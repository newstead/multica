package service

import (
	"context"
	"encoding/json"
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
