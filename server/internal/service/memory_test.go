package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_recall_sample WHERE workspace_id = $1`, workspaceID)
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

func TestMemoryDispatchDueDeliveriesIsPerProvider(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	cleanupMemoryServiceRows(t, pool, workspaceID)

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

	now := time.Now().UTC().Add(5 * time.Minute)
	hindsight := &fakeMemoryProvider{name: "hindsight", providerMemoryID: "hindsight-memory-1"}
	mem0 := &fakeMemoryProvider{name: "mem0", retainErr: errors.New("mem0 unavailable")}
	svc := NewMemoryService(queries, pool)
	svc.Clock = func() time.Time { return now }
	svc.MaxDeliveryAttempts = 1
	svc.Providers = map[string]MemoryProvider{"hindsight": hindsight, "mem0": mem0}

	retain, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "dispatch-key-" + uuid.NewString(),
		CorrelationID:  "correlation-1",
		SourceID:       "source-1",
		Content:        json.RawMessage(`{"text":"remember this"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if got, want := len(retain.Deliveries), 2; got != want {
		t.Fatalf("deliveries = %d, want %d", got, want)
	}

	results, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10)
	if err != nil {
		t.Fatalf("dispatch due deliveries: %v", err)
	}
	if got, want := len(results), 2; got != want {
		t.Fatalf("dispatch results = %d, want %d", got, want)
	}
	if len(hindsight.retained) != 1 || len(mem0.retained) != 1 {
		t.Fatalf("provider calls hindsight=%d mem0=%d, want 1 each", len(hindsight.retained), len(mem0.retained))
	}
	for provider, events := range map[string][]MemoryEventEnvelope{"hindsight": hindsight.retained, "mem0": mem0.retained} {
		if events[0].CorrelationID != "correlation-1" || events[0].SourceID != "source-1" {
			t.Fatalf("%s envelope correlation/source = %q/%q, want correlation-1/source-1", provider, events[0].CorrelationID, events[0].SourceID)
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT provider, status, attempt_count, delivery_lag_ms, error
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2
	`, workspaceID, retain.Event.ID)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	defer rows.Close()

	statuses := map[string]string{}
	errorsByProvider := map[string]pgtype.Text{}
	for rows.Next() {
		var provider, status string
		var attempts int32
		var lag int64
		var providerErr pgtype.Text
		if err := rows.Scan(&provider, &status, &attempts, &lag, &providerErr); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		statuses[provider] = status
		errorsByProvider[provider] = providerErr
		if attempts != 1 {
			t.Fatalf("%s attempts = %d, want 1", provider, attempts)
		}
		if lag < 0 {
			t.Fatalf("%s lag = %d, want non-negative", provider, lag)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate deliveries: %v", err)
	}
	if statuses["hindsight"] != "delivered" || statuses["mem0"] != "terminal_failed" {
		t.Fatalf("statuses = %#v, want hindsight delivered and mem0 terminal_failed", statuses)
	}
	if errorsByProvider["hindsight"].Valid || !errorsByProvider["mem0"].Valid {
		t.Fatalf("delivery errors = %#v, want only mem0 error", errorsByProvider)
	}
}

func TestMemoryRecallDualRecordsPairedSamples(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	cleanupMemoryServiceRows(t, pool, workspaceID)

	if _, err := queries.UpsertMemoryWorkspaceConfig(ctx, db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  workspaceID,
		Enabled:                      true,
		PrimaryProvider:              "hindsight",
		ShadowProvider:               pgtype.Text{String: "mem0", Valid: true},
		ReadMode:                     "dual",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	svc := NewMemoryService(queries, pool)
	svc.Providers = map[string]MemoryProvider{
		"hindsight": &fakeMemoryProvider{name: "hindsight", recallResults: json.RawMessage(`[{"id":"primary-only"}]`)},
		"mem0":      &fakeMemoryProvider{name: "mem0", recallResults: json.RawMessage(`[{"id":"shadow-only"}]`)},
	}
	result, err := svc.Recall(ctx, MemoryRecallRequest{
		Scope:         MemoryScope{WorkspaceID: workspaceID},
		ReadMode:      "dual",
		CorrelationID: "recall-pair-1",
		Query:         "compare scoped memory",
		Limit:         5,
	})
	if err != nil {
		t.Fatalf("recall dual: %v", err)
	}
	if result.Mode != "dual" || result.CorrelationID != "recall-pair-1" {
		t.Fatalf("recall mode/correlation = %q/%q, want dual/recall-pair-1", result.Mode, result.CorrelationID)
	}
	if result.Primary == nil || result.Shadow == nil {
		t.Fatalf("dual recall returned primary=%#v shadow=%#v, want both", result.Primary, result.Shadow)
	}
	if string(result.Primary.Result.Results) != `[{"id":"primary-only"}]` || string(result.Shadow.Result.Results) != `[{"id":"shadow-only"}]` {
		t.Fatalf("dual recall results were not kept provider-separated: primary=%s shadow=%s", result.Primary.Result.Results, result.Shadow.Result.Results)
	}

	rows, err := pool.Query(ctx, `
		SELECT provider, recall_correlation_id, read_mode
		FROM memory_recall_sample
		WHERE workspace_id = $1 AND recall_correlation_id = 'recall-pair-1'
	`, workspaceID)
	if err != nil {
		t.Fatalf("list recall samples: %v", err)
	}
	defer rows.Close()

	samples := map[string]string{}
	for rows.Next() {
		var provider, correlationID, readMode string
		if err := rows.Scan(&provider, &correlationID, &readMode); err != nil {
			t.Fatalf("scan recall sample: %v", err)
		}
		if correlationID != "recall-pair-1" || readMode != "dual" {
			t.Fatalf("sample %s correlation/read_mode = %q/%q, want recall-pair-1/dual", provider, correlationID, readMode)
		}
		samples[provider] = readMode
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recall samples: %v", err)
	}
	if len(samples) != 2 || samples["hindsight"] != "dual" || samples["mem0"] != "dual" {
		t.Fatalf("samples = %#v, want paired hindsight and mem0 samples", samples)
	}
}

func cleanupMemoryServiceRows(t *testing.T, pool *pgxpool.Pool, workspaceID pgtype.UUID) {
	t.Helper()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_recall_sample WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_provider_delivery WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_event WHERE workspace_id = $1`, workspaceID)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM memory_workspace_config WHERE workspace_id = $1`, workspaceID)
	})
}

type fakeMemoryProvider struct {
	name             string
	providerMemoryID string
	retainErr        error
	recallErr        error
	recallResults    json.RawMessage
	retained         []MemoryEventEnvelope
}

func (p *fakeMemoryProvider) Name() string { return p.name }

func (p *fakeMemoryProvider) Retain(_ context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	p.retained = append(p.retained, event)
	if p.retainErr != nil {
		return MemoryProviderResult{}, p.retainErr
	}
	return MemoryProviderResult{ProviderMemoryID: p.providerMemoryID, Response: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *fakeMemoryProvider) Recall(_ context.Context, req MemoryRecallRequest) (MemoryRecallResult, error) {
	if p.recallErr != nil {
		return MemoryRecallResult{}, p.recallErr
	}
	results := p.recallResults
	if len(results) == 0 {
		results = json.RawMessage(`[]`)
	}
	return MemoryRecallResult{Provider: req.Provider, Results: results, Provenance: json.RawMessage(`{"source":"test"}`)}, nil
}

func (p *fakeMemoryProvider) Update(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	return p.Retain(ctx, event)
}

func (p *fakeMemoryProvider) Invalidate(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	return p.Retain(ctx, event)
}

func (p *fakeMemoryProvider) Delete(ctx context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	return p.Retain(ctx, event)
}

func (p *fakeMemoryProvider) Health(context.Context) (MemoryProviderHealth, error) {
	return MemoryProviderHealth{Provider: p.name, OK: true}, nil
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
		   AND EXISTS (
		       SELECT 1 FROM information_schema.columns
		       WHERE table_schema = 'public'
		         AND table_name = 'memory_provider_delivery'
		         AND column_name = 'delivery_lag_ms'
		   )
		   AND EXISTS (
		       SELECT 1 FROM information_schema.columns
		       WHERE table_schema = 'public'
		         AND table_name = 'memory_recall_sample'
		         AND column_name = 'recall_correlation_id'
		   )
	`).Scan(&migrated); err != nil {
		pool.Close()
		t.Skipf("memory migration check failed: %v", err)
	}
	if !migrated {
		pool.Close()
		t.Skip("memory tables not present or latest memory migration is not applied")
	}

	t.Cleanup(pool.Close)
	return pool
}
