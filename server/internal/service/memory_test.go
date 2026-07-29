package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestMemoryEventEnvelopeIdempotencyIsStable(t *testing.T) {
	workspaceID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	req := MemoryRetainRequest{
		Scope:     MemoryScope{WorkspaceID: workspaceID},
		Actor:     MemoryActor{Type: "system"},
		EventType: "retain",
		Content:   json.RawMessage(`{"text":"remember this"}`),
	}
	_, a, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		t.Fatalf("BuildMemoryEventEnvelope: %v", err)
	}
	_, b, err := BuildMemoryEventEnvelope(req)
	if err != nil {
		t.Fatalf("BuildMemoryEventEnvelope second call: %v", err)
	}
	if MemoryIdempotencyKey(a) != MemoryIdempotencyKey(b) {
		t.Fatal("idempotency key must be stable for the same canonical envelope")
	}
}

func TestMemoryDeliveryFailureStateTerminalAfterMaxAttempts(t *testing.T) {
	fixed := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	svc := &MemoryService{
		Clock:               func() time.Time { return fixed },
		MaxDeliveryAttempts: 2,
		BaseBackoff:         time.Minute,
	}
	status, next, terminal := svc.DeliveryFailureState(1)
	if status != "retry" || !next.Valid || terminal.Valid {
		t.Fatalf("attempt 1 = status %q next %v terminal %v, want retry with next only", status, next, terminal)
	}
	status, next, terminal = svc.DeliveryFailureState(2)
	if status != "terminal_failed" || next.Valid || !terminal.Valid {
		t.Fatalf("attempt 2 = status %q next %v terminal %v, want terminal_failed with terminal_at", status, next, terminal)
	}
}

func TestMemoryRetainDuplicateRepairsProviderDeliverySet(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_provider_delivery WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_event WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_workspace_config WHERE workspace_id = $1`, workspaceID)
	})

	if _, err := queries.UpsertMemoryWorkspaceConfig(ctx, db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  workspaceID,
		Enabled:                      true,
		PrimaryProvider:              "hindsight",
		ShadowProvider:               pgtype.Text{String: "mem0", Valid: true},
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	svc := NewMemoryService(queries, pool)
	svc.Clock = func() time.Time { return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC) }
	req := MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "retain-" + uuid.NewString(),
		Content:        json.RawMessage(`{"text":"remember this"}`),
	}

	first, err := svc.Retain(ctx, req)
	if err != nil {
		t.Fatalf("first retain: %v", err)
	}
	if !first.Inserted {
		t.Fatal("first retain should insert the event")
	}
	if got, want := len(first.Deliveries), 2; got != want {
		t.Fatalf("first retain deliveries = %d, want %d", got, want)
	}

	tag, err := pool.Exec(ctx, `
		DELETE FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'mem0'
	`, workspaceID, first.Event.ID)
	if err != nil {
		t.Fatalf("delete shadow delivery fixture: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("delete shadow delivery affected %d rows, want 1", tag.RowsAffected())
	}

	duplicate, err := svc.Retain(ctx, req)
	if err != nil {
		t.Fatalf("duplicate retain: %v", err)
	}
	if duplicate.Inserted {
		t.Fatal("duplicate retain should return the existing event")
	}
	if duplicate.Event.ID != first.Event.ID {
		t.Fatalf("duplicate event ID = %v, want %v", duplicate.Event.ID, first.Event.ID)
	}
	if got, want := len(duplicate.Deliveries), 2; got != want {
		t.Fatalf("duplicate retain deliveries = %d, want %d", got, want)
	}

	returned := map[string]bool{}
	for _, delivery := range duplicate.Deliveries {
		returned[delivery.Provider] = true
	}
	for _, provider := range []string{"hindsight", "mem0"} {
		if !returned[provider] {
			t.Fatalf("duplicate retain did not return provider delivery %q: %#v", provider, duplicate.Deliveries)
		}
	}

	var eventCount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM memory_event
		WHERE workspace_id = $1 AND idempotency_key = $2
	`, workspaceID, req.IdempotencyKey).Scan(&eventCount); err != nil {
		t.Fatalf("count memory events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("memory_event count = %d, want 1", eventCount)
	}

	rows, err := pool.Query(ctx, `
		SELECT provider, count(*)
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2
		GROUP BY provider
	`, workspaceID, first.Event.ID)
	if err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	defer rows.Close()

	counts := map[string]int64{}
	for rows.Next() {
		var provider string
		var count int64
		if err := rows.Scan(&provider, &count); err != nil {
			t.Fatalf("scan delivery count: %v", err)
		}
		counts[provider] = count
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate delivery counts: %v", err)
	}
	if len(counts) != 2 || counts["hindsight"] != 1 || counts["mem0"] != 1 {
		t.Fatalf("delivery counts = %#v, want exactly one hindsight and one mem0", counts)
	}
}

func newMemoryServiceIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}

	var migrated bool
	if err := pool.QueryRow(ctx, `
		SELECT to_regclass('public.memory_workspace_config') IS NOT NULL
		   AND to_regclass('public.memory_event') IS NOT NULL
		   AND to_regclass('public.memory_provider_delivery') IS NOT NULL
	`).Scan(&migrated); err != nil {
		pool.Close()
		t.Skipf("memory migration check failed: %v", err)
	}
	if !migrated {
		pool.Close()
		t.Skip("memory tables not present; database is not migrated")
	}

	t.Cleanup(pool.Close)
	return pool
}

func TestMemoryCapturePolicyAllowsOnlyApprovedVisibleSources(t *testing.T) {
	workspaceID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	commentID := util.MustParseUUID("22222222-2222-2222-2222-222222222222")
	base := MemoryCaptureSource{
		SourceType: MemorySourceHumanComment,
		SourceID:   commentID,
		Scope:      MemoryScope{WorkspaceID: workspaceID},
		Actor:      MemoryActor{Type: "member"},
		Text:       "remember this visible project decision",
	}
	if _, ok := BuildApprovedMemoryRetainRequest(base); !ok {
		t.Fatal("approved human comment should pass capture policy")
	}
	base.SourceType = "arbitrary_attachment"
	if _, ok := BuildApprovedMemoryRetainRequest(base); ok {
		t.Fatal("arbitrary attachments must not pass capture policy")
	}
	base.SourceType = MemorySourceHumanComment
	base.Text = "password=super-secret"
	if _, ok := BuildApprovedMemoryRetainRequest(base); ok {
		t.Fatal("secret-like content must not pass capture policy")
	}
}

func TestMemoryCapturePolicyUsesContentRevisionInKey(t *testing.T) {
	workspaceID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	issueID := util.MustParseUUID("22222222-2222-2222-2222-222222222222")
	src := MemoryCaptureSource{
		SourceType: MemorySourceIssueDescription,
		SourceID:   issueID,
		Scope:      MemoryScope{WorkspaceID: workspaceID, IssueID: issueID},
		Actor:      MemoryActor{Type: "member"},
		Text:       "first description",
	}
	first, ok := BuildApprovedMemoryRetainRequest(src)
	if !ok {
		t.Fatal("first issue description should pass capture policy")
	}
	src.Text = "second description"
	second, ok := BuildApprovedMemoryRetainRequest(src)
	if !ok {
		t.Fatal("second issue description should pass capture policy")
	}
	if first.IdempotencyKey == second.IdempotencyKey {
		t.Fatalf("idempotency key did not change across content revisions: %s", first.IdempotencyKey)
	}
	if first.Metadata["content_sha256"] == "" || first.Metadata["content_sha256"] == second.Metadata["content_sha256"] {
		t.Fatalf("content hashes did not track revisions: first=%#v second=%#v", first.Metadata, second.Metadata)
	}
}

func TestMemoryCaptureRedactsSecretsAndRejectsRawExecutionLogs(t *testing.T) {
	workspaceID := util.MustParseUUID("11111111-1111-1111-1111-111111111111")
	commentID := util.MustParseUUID("22222222-2222-2222-2222-222222222222")
	req, ok := BuildApprovedMemoryRetainRequest(MemoryCaptureSource{
		SourceType: MemorySourceHumanComment,
		SourceID:   commentID,
		Scope:      MemoryScope{WorkspaceID: workspaceID},
		Actor:      MemoryActor{Type: "member"},
		Text:       "PASSWORD: hunter2 ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa sk-abcdefghijklmnopqrstuvwxyz",
	})
	if !ok {
		t.Fatal("redactable visible comment should pass capture policy")
	}
	var content map[string]any
	if err := json.Unmarshal(req.Content, &content); err != nil {
		t.Fatalf("unmarshal retained content: %v", err)
	}
	text, _ := content["text"].(string)
	for _, leaked := range []string{"hunter2", "ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "sk-abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("retained text leaked %q: %s", leaked, text)
		}
	}
	if !strings.Contains(text, "[REDACTED") {
		t.Fatalf("retained text did not use canonical redaction markers: %s", text)
	}

	_, ok = BuildApprovedMemoryRetainRequest(MemoryCaptureSource{
		SourceType: MemorySourceHumanComment,
		SourceID:   commentID,
		Scope:      MemoryScope{WorkspaceID: workspaceID},
		Actor:      MemoryActor{Type: "member"},
		Text:       "Chunk ID: abc123\nWall time: 0.1s\nProcess exited with code 0\nOutput:\nsecret-ish log",
	})
	if ok {
		t.Fatal("raw execution logs must not pass memory capture policy")
	}
}

func TestMemoryTokenBudgetTruncatesCJKAndUnbrokenText(t *testing.T) {
	for _, text := range []string{strings.Repeat("记", 80), strings.Repeat("a", 80)} {
		got := truncateApproxTokens(text, 16)
		if got == text {
			t.Fatalf("text was not truncated: %q", text[:16])
		}
		if tokens := approxTokenCount(got); tokens > 16 {
			t.Fatalf("truncated token count = %d, want <= 16 for %q", tokens, got)
		}
	}
}

func TestMemoryRecallForTaskFindsDurableAgentOutcomeAcrossTasks(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	agentID := util.MustParseUUID(uuid.NewString())
	otherAgentID := util.MustParseUUID(uuid.NewString())
	issueID := util.MustParseUUID(uuid.NewString())
	otherIssueID := util.MustParseUUID(uuid.NewString())
	taskAID := util.MustParseUUID(uuid.NewString())
	taskBID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_provider_delivery WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_event WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_workspace_config WHERE workspace_id = $1`, workspaceID)
	})
	if _, err := queries.UpsertMemoryWorkspaceConfig(ctx, db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  workspaceID,
		Enabled:                      true,
		PrimaryProvider:              "hindsight",
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	svc := NewMemoryService(queries, pool)
	if _, err := svc.Retain(ctx, mustMemoryRetain(t, MemoryCaptureSource{
		SourceType: MemorySourceAgentOutcomeSummary,
		SourceID:   taskAID,
		Scope:      MemoryScope{WorkspaceID: workspaceID, AgentID: agentID, IssueID: issueID},
		Actor:      MemoryActor{Type: "agent", ID: agentID},
		Text:       "final outcome should be durable for the next task",
	})); err != nil {
		t.Fatalf("retain outcome memory: %v", err)
	}

	items, err := svc.RecallForTask(ctx, MemoryRecallForTaskRequest{
		Scope: MemoryScope{WorkspaceID: workspaceID, AgentID: agentID, IssueID: issueID, TaskID: taskBID},
		Limit: 4,
	})
	if err != nil {
		t.Fatalf("recall same issue next task: %v", err)
	}
	if len(items) != 1 || items[0].SourceID != util.UUIDToString(taskAID) || items[0].Scope.TaskID != "" {
		t.Fatalf("durable outcome recall = %#v, want task A source with no task-scoped recall", items)
	}
	for name, scope := range map[string]MemoryScope{
		"other agent": {WorkspaceID: workspaceID, AgentID: otherAgentID, IssueID: issueID, TaskID: taskBID},
		"other issue": {WorkspaceID: workspaceID, AgentID: agentID, IssueID: otherIssueID, TaskID: taskBID},
	} {
		items, err := svc.RecallForTask(ctx, MemoryRecallForTaskRequest{Scope: scope, Limit: 4})
		if err != nil {
			t.Fatalf("recall %s: %v", name, err)
		}
		if len(items) != 0 {
			t.Fatalf("recall %s leaked outcome memory: %#v", name, items)
		}
	}
}

func TestMemoryRecallForTaskScopesAndTruncates(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	otherWorkspaceID := util.MustParseUUID(uuid.NewString())
	issueID := util.MustParseUUID(uuid.NewString())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, ws := range []pgtype.UUID{workspaceID, otherWorkspaceID} {
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_provider_delivery WHERE workspace_id = $1`, ws)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_event WHERE workspace_id = $1`, ws)
			_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_workspace_config WHERE workspace_id = $1`, ws)
		}
	})
	for _, ws := range []pgtype.UUID{workspaceID, otherWorkspaceID} {
		if _, err := queries.UpsertMemoryWorkspaceConfig(ctx, db.UpsertMemoryWorkspaceConfigParams{
			WorkspaceID:                  ws,
			Enabled:                      true,
			PrimaryProvider:              "hindsight",
			ReadMode:                     "primary",
			ProviderSettings:             []byte(`{}`),
			ProviderCredentialsEncrypted: []byte(`{}`),
		}); err != nil {
			t.Fatalf("seed memory config: %v", err)
		}
	}

	svc := NewMemoryService(queries, pool)
	longText := strings.Repeat("scoped ", 80)
	if _, err := svc.Retain(ctx, mustMemoryRetain(t, MemoryCaptureSource{
		SourceType: MemorySourceHumanComment,
		SourceID:   util.MustParseUUID(uuid.NewString()),
		Scope:      MemoryScope{WorkspaceID: workspaceID, IssueID: issueID},
		Actor:      MemoryActor{Type: "member"},
		Text:       longText,
	})); err != nil {
		t.Fatalf("retain scoped memory: %v", err)
	}
	if _, err := svc.Retain(ctx, mustMemoryRetain(t, MemoryCaptureSource{
		SourceType: MemorySourceHumanComment,
		SourceID:   util.MustParseUUID(uuid.NewString()),
		Scope:      MemoryScope{WorkspaceID: otherWorkspaceID},
		Actor:      MemoryActor{Type: "member"},
		Text:       "foreign workspace memory must not appear",
	})); err != nil {
		t.Fatalf("retain foreign memory: %v", err)
	}

	items, err := svc.RecallForTask(ctx, MemoryRecallForTaskRequest{
		Scope:       MemoryScope{WorkspaceID: workspaceID, IssueID: issueID},
		TokenBudget: 40,
		Limit:       4,
	})
	if err != nil {
		t.Fatalf("RecallForTask: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("recall items = %d, want 1 scoped item: %#v", len(items), items)
	}
	if items[0].Provider != memoryAuditLogFallbackProvider {
		t.Fatalf("recall provider = %q, want truthful fallback provider", items[0].Provider)
	}
	if items[0].Scope.WorkspaceID != util.UUIDToString(workspaceID) || items[0].Scope.IssueID != util.UUIDToString(issueID) {
		t.Fatalf("wrong recall scope: %#v", items[0].Scope)
	}
	if strings.Contains(items[0].Text, "foreign workspace") {
		t.Fatal("recall leaked foreign workspace memory")
	}
	if got := len(strings.Fields(items[0].Text)); got > 20 {
		t.Fatalf("recall text was not token-truncated enough: %d words", got)
	}
}

func mustMemoryRetain(t *testing.T, src MemoryCaptureSource) MemoryRetainRequest {
	t.Helper()
	req, ok := BuildApprovedMemoryRetainRequest(src)
	if !ok {
		t.Fatalf("source did not pass capture policy: %#v", src)
	}
	return req
}
