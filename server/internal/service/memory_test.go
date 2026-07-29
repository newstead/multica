package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
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

func TestMemoryDispatchDueDeliveriesRecoversRetriedProvider(t *testing.T) {
	pool := newMemoryServiceIntegrationPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	queries := db.New(pool)
	workspaceID := util.MustParseUUID(uuid.NewString())
	cleanupMemoryServiceRows(t, pool, workspaceID)

	if _, err := queries.UpsertMemoryWorkspaceConfig(ctx, db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  workspaceID,
		Enabled:                      true,
		PrimaryProvider:              "mem0",
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	now := time.Now().UTC().Add(5 * time.Minute)
	mem0 := &fakeMemoryProvider{name: "mem0", retainErrs: []error{errors.New("mem0 temporary outage")}}
	svc := NewMemoryService(queries, pool)
	svc.Clock = func() time.Time { return now }
	svc.MaxDeliveryAttempts = 2
	svc.BaseBackoff = time.Minute
	svc.Providers = map[string]MemoryProvider{"mem0": mem0}

	retain, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "retry-key-" + uuid.NewString(),
		Content:        json.RawMessage(`{"text":"retry this"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}

	if _, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	var status string
	var attempts int32
	if err := pool.QueryRow(ctx, `
		SELECT status, attempt_count
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'mem0'
	`, workspaceID, retain.Event.ID).Scan(&status, &attempts); err != nil {
		t.Fatalf("read retry delivery: %v", err)
	}
	if status != "retry" || attempts != 1 {
		t.Fatalf("after first dispatch status/attempts = %q/%d, want retry/1", status, attempts)
	}

	now = now.Add(2 * time.Minute)
	if _, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	var providerErr pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT status, attempt_count, error
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'mem0'
	`, workspaceID, retain.Event.ID).Scan(&status, &attempts, &providerErr); err != nil {
		t.Fatalf("read recovered delivery: %v", err)
	}
	if status != "delivered" || attempts != 2 || providerErr.Valid {
		t.Fatalf("after recovery status/attempts/error = %q/%d/%v, want delivered/2/no error", status, attempts, providerErr)
	}
}

func TestMemoryDispatchDueDeliveriesOrdersByNextAttempt(t *testing.T) {
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
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	now := time.Now().UTC().Add(5 * time.Minute)
	provider := &fakeMemoryProvider{name: "hindsight"}
	svc := NewMemoryService(queries, pool)
	svc.Clock = func() time.Time { return now }
	svc.Providers = map[string]MemoryProvider{"hindsight": provider}

	first, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "order-first-" + uuid.NewString(),
		SourceID:       "first",
		Content:        json.RawMessage(`{"text":"first"}`),
	})
	if err != nil {
		t.Fatalf("retain first: %v", err)
	}
	second, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "order-second-" + uuid.NewString(),
		SourceID:       "second",
		Content:        json.RawMessage(`{"text":"second"}`),
	})
	if err != nil {
		t.Fatalf("retain second: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE memory_provider_delivery
		SET next_attempt_at = CASE memory_event_id WHEN $2::uuid THEN $4::timestamptz ELSE $3::timestamptz END
		WHERE workspace_id = $1 AND memory_event_id IN ($2, $5)
	`, workspaceID, first.Event.ID, now.Add(time.Minute), now.Add(2*time.Minute), second.Event.ID); err != nil {
		t.Fatalf("adjust delivery order fixture: %v", err)
	}

	now = now.Add(3 * time.Minute)
	if _, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10); err != nil {
		t.Fatalf("dispatch ordered deliveries: %v", err)
	}
	if got, want := len(provider.retained), 2; got != want {
		t.Fatalf("retained calls = %d, want %d", got, want)
	}
	if provider.retained[0].SourceID != "second" || provider.retained[1].SourceID != "first" {
		t.Fatalf("dispatch source order = %q then %q, want second then first", provider.retained[0].SourceID, provider.retained[1].SourceID)
	}
}

func TestMemoryDispatchDeleteEventUsesProviderDelete(t *testing.T) {
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
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	provider := &fakeMemoryProvider{name: "hindsight"}
	svc := NewMemoryService(queries, pool)
	svc.Clock = func() time.Time { return time.Now().UTC().Add(5 * time.Minute) }
	svc.Providers = map[string]MemoryProvider{"hindsight": provider}

	retain, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "delete",
		IdempotencyKey: "delete-key-" + uuid.NewString(),
		Content:        json.RawMessage(`{"memory_id":"provider-record-1"}`),
	})
	if err != nil {
		t.Fatalf("queue delete: %v", err)
	}
	if _, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10); err != nil {
		t.Fatalf("dispatch delete: %v", err)
	}
	if len(provider.deleted) != 1 || len(provider.retained) != 0 {
		t.Fatalf("provider retained/deleted calls = %d/%d, want 0/1", len(provider.retained), len(provider.deleted))
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT status
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'hindsight'
	`, workspaceID, retain.Event.ID).Scan(&status); err != nil {
		t.Fatalf("read delete delivery: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("delete delivery status = %q, want delivered", status)
	}
}

func TestMemoryDispatchDueDeliveriesConcurrentClaimDoesNotDuplicateProviderMemory(t *testing.T) {
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
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	}); err != nil {
		t.Fatalf("seed memory config: %v", err)
	}

	started := make(chan struct{})
	unblock := make(chan struct{})
	provider := &fakeMemoryProvider{name: "hindsight", providerMemoryID: "provider-memory-1", retainStarted: started, retainBlock: unblock}
	svc := NewMemoryService(queries, pool)
	svc.DeliveryLeaseTimeout = time.Hour
	svc.Providers = map[string]MemoryProvider{"hindsight": provider}

	retain, err := svc.Retain(ctx, MemoryRetainRequest{
		Scope:          MemoryScope{WorkspaceID: workspaceID},
		Actor:          MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "concurrent-claim-" + uuid.NewString(),
		Content:        json.RawMessage(`{"text":"claim once"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}

	type dispatchResult struct {
		results []MemoryDispatchResult
		err     error
	}
	firstDone := make(chan dispatchResult, 1)
	go func() {
		results, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10)
		firstDone <- dispatchResult{results: results, err: err}
	}()

	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first dispatch did not reach provider")
	}
	second, err := svc.DispatchDueMemoryProviderDeliveries(ctx, workspaceID, 10)
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second dispatch claimed %d deliveries while first worker owned the row", len(second))
	}

	close(unblock)
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first dispatch: %v", first.err)
	}
	if len(first.results) != 1 {
		t.Fatalf("first dispatch results = %d, want 1", len(first.results))
	}
	if len(provider.retained) != 1 {
		t.Fatalf("provider retain calls = %d, want 1", len(provider.retained))
	}

	// A stale explicit reclaim after the row is delivered must not call the
	// provider again or regress the durable delivery row.
	if _, err := svc.DispatchMemoryProviderDelivery(ctx, workspaceID, retain.Deliveries[0].ID); err != nil {
		t.Fatalf("stale explicit reclaim: %v", err)
	}
	if len(provider.retained) != 1 {
		t.Fatalf("provider retain calls after stale reclaim = %d, want 1", len(provider.retained))
	}
	var status string
	var attempts int32
	var providerMemoryID pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT status, attempt_count, provider_memory_id
		FROM memory_provider_delivery
		WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'hindsight'
	`, workspaceID, retain.Event.ID).Scan(&status, &attempts, &providerMemoryID); err != nil {
		t.Fatalf("read final delivery: %v", err)
	}
	if status != "delivered" || attempts != 1 || providerMemoryID.String != "provider-memory-1" {
		t.Fatalf("final delivery status/attempts/provider_memory_id = %q/%d/%q, want delivered/1/provider-memory-1", status, attempts, providerMemoryID.String)
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
	retainErrs       []error
	recallErr        error
	recallResults    json.RawMessage
	retained         []MemoryEventEnvelope
	retainStarted    chan struct{}
	retainBlock      chan struct{}
	retainStartOnce  sync.Once
	updated          []MemoryEventEnvelope
	invalidated      []MemoryEventEnvelope
	deleted          []MemoryEventEnvelope
}

func (p *fakeMemoryProvider) Name() string { return p.name }

func (p *fakeMemoryProvider) Retain(_ context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	p.retained = append(p.retained, event)
	if p.retainStarted != nil {
		p.retainStartOnce.Do(func() { close(p.retainStarted) })
	}
	if p.retainBlock != nil {
		<-p.retainBlock
	}
	if len(p.retainErrs) > 0 {
		err := p.retainErrs[0]
		p.retainErrs = p.retainErrs[1:]
		if err != nil {
			return MemoryProviderResult{}, err
		}
	}
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

func (p *fakeMemoryProvider) Update(_ context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	p.updated = append(p.updated, event)
	return MemoryProviderResult{ProviderMemoryID: p.providerMemoryID, Response: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *fakeMemoryProvider) Invalidate(_ context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	p.invalidated = append(p.invalidated, event)
	return MemoryProviderResult{ProviderMemoryID: p.providerMemoryID, Response: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *fakeMemoryProvider) Delete(_ context.Context, event MemoryEventEnvelope) (MemoryProviderResult, error) {
	p.deleted = append(p.deleted, event)
	return MemoryProviderResult{ProviderMemoryID: p.providerMemoryID, Response: json.RawMessage(`{"ok":true}`)}, nil
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
