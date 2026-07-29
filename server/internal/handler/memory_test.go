package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func memoryHandlerRequest(method, path string, body any) *http.Request {
	req := newRequest(method, path, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testWorkspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func cleanupMemoryGatewayTestRows(t *testing.T, workspaceIDs ...string) {
	t.Helper()
	ctx := context.Background()
	for _, workspaceID := range workspaceIDs {
		if workspaceID == "" {
			continue
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM memory_recall_sample WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup memory_recall_sample: %v", err)
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM memory_provider_delivery WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup memory_provider_delivery: %v", err)
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM memory_event WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup memory_event: %v", err)
		}
		if _, err := testPool.Exec(ctx, `DELETE FROM memory_workspace_config WHERE workspace_id = $1`, workspaceID); err != nil {
			t.Fatalf("cleanup memory_workspace_config: %v", err)
		}
	}
}

func enableMemoryConfigForTest(t *testing.T, workspaceID, primary, shadow string) {
	t.Helper()
	shadowText := pgtype.Text{}
	if shadow != "" {
		shadowText = pgtype.Text{String: shadow, Valid: true}
	}
	_, err := testHandler.Queries.UpsertMemoryWorkspaceConfig(context.Background(), db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  parseUUID(workspaceID),
		Enabled:                      true,
		PrimaryProvider:              primary,
		ShadowProvider:               shadowText,
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{"capture":"test"}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("enable memory config: %v", err)
	}
}

func TestNewRegistersMemoryProvidersFromConfig(t *testing.T) {
	h := New(nil, nil, nil, nil, nil, nil, nil, nil, Config{
		MemoryHindsightBaseURL: "https://hindsight.example.test",
		MemoryMem0BaseURL:      "https://mem0.example.test",
		MemoryMem0APIKey:       "mem0-test-key",
	})
	if h.MemoryService == nil {
		t.Fatal("MemoryService is nil")
	}
	if _, ok := h.MemoryService.Providers["hindsight"]; !ok {
		t.Fatalf("hindsight provider was not registered: %#v", h.MemoryService.Providers)
	}
	if _, ok := h.MemoryService.Providers[service.Mem0ProviderName]; !ok {
		t.Fatalf("mem0 provider was not registered: %#v", h.MemoryService.Providers)
	}
	if h.MemoryDeliveryWorker == nil {
		t.Fatal("MemoryDeliveryWorker is nil")
	}
}

func TestMemoryDeliveryWorkerRunStopsBoundedPool(t *testing.T) {
	worker := NewMemoryDeliveryWorker(testHandler)
	ctx, cancel := context.WithCancel(context.Background())
	go worker.Run(ctx)
	cancel()
	if !worker.WaitWithTimeout(time.Second) {
		t.Fatal("bounded memory delivery worker pool did not stop after cancellation")
	}
}

func TestGetMemoryConfigHonorsReleaseFlag(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	w := httptest.NewRecorder()
	testHandler.GetMemoryConfig(w, memoryHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory/config", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("GetMemoryConfig flag off: expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetMemoryConfigRedactsCredentials(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	_, err := testHandler.Queries.UpsertMemoryWorkspaceConfig(context.Background(), db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  parseUUID(testWorkspaceID),
		Enabled:                      true,
		PrimaryProvider:              "hindsight",
		ReadMode:                     "primary",
		ProviderSettings:             []byte(`{"capture":"test"}`),
		ProviderCredentialsEncrypted: []byte(`{"hindsight":"ciphertext-secret"}`),
	})
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}
	w := httptest.NewRecorder()
	testHandler.GetMemoryConfig(w, memoryHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory/config", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemoryConfig: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "ciphertext-secret") || strings.Contains(body, "provider_credentials") {
		t.Fatalf("GetMemoryConfig leaked backend-only credentials: %s", body)
	}
}

func TestCreateMemoryRetainEventIsIdempotent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "hindsight", "")

	body := map[string]any{
		"event_type":      "retain",
		"idempotency_key": "dup-retain-key",
		"actor_type":      "system",
		"content":         map[string]any{"fact": "Alice prefers concise updates"},
	}
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		testHandler.CreateMemoryRetainEvent(w, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/events/retain", body))
		if i == 0 && w.Code != http.StatusCreated {
			t.Fatalf("first retain: expected 201, got %d: %s", w.Code, w.Body.String())
		}
		if i == 1 && w.Code != http.StatusOK {
			t.Fatalf("duplicate retain: expected 200, got %d: %s", w.Code, w.Body.String())
		}
	}
	var eventCount, deliveryCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM memory_event WHERE workspace_id = $1 AND idempotency_key = $2`, testWorkspaceID, "dup-retain-key").Scan(&eventCount); err != nil {
		t.Fatalf("count memory events: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM memory_provider_delivery WHERE workspace_id = $1`, testWorkspaceID).Scan(&deliveryCount); err != nil {
		t.Fatalf("count memory deliveries: %v", err)
	}
	if eventCount != 1 || deliveryCount != 1 {
		t.Fatalf("duplicate retain wrote event_count=%d delivery_count=%d, want 1/1", eventCount, deliveryCount)
	}
}

func TestCreateMemoryRetainEventCapturesExplicitFeedbackSource(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "hindsight", "")

	sourceID := "11111111-2222-3333-4444-555555555555"
	body := map[string]any{
		"source_type": "explicit_feedback",
		"source_id":   sourceID,
		"actor_type":  "member",
		"actor_id":    testUserID,
		"text":        "Please remember this explicit memory preference. PASSWORD: hunter2",
	}
	w := httptest.NewRecorder()
	testHandler.CreateMemoryRetainEvent(w, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/events/retain", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("explicit feedback retain: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var sourceType, retainedText string
	if err := testPool.QueryRow(context.Background(), `
		SELECT envelope->'content'->>'source_type', envelope->'content'->>'text'
		FROM memory_event
		WHERE workspace_id = $1 AND envelope->'content'->>'source_id' = $2
	`, testWorkspaceID, sourceID).Scan(&sourceType, &retainedText); err != nil {
		t.Fatalf("query explicit feedback memory: %v", err)
	}
	if sourceType != "explicit_feedback" {
		t.Fatalf("source_type = %q, want explicit_feedback", sourceType)
	}
	if strings.Contains(retainedText, "hunter2") || !strings.Contains(retainedText, "[REDACTED") {
		t.Fatalf("explicit feedback text was not safely redacted: %s", retainedText)
	}
}

func TestCreateMemoryRecallEndpointKeepsDualResultsSeparate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	_, err := testHandler.Queries.UpsertMemoryWorkspaceConfig(context.Background(), db.UpsertMemoryWorkspaceConfigParams{
		WorkspaceID:                  parseUUID(testWorkspaceID),
		Enabled:                      true,
		PrimaryProvider:              "hindsight",
		ShadowProvider:               pgtype.Text{String: "mem0", Valid: true},
		ReadMode:                     "dual",
		ProviderSettings:             []byte(`{"capture":"test"}`),
		ProviderCredentialsEncrypted: []byte(`{}`),
	})
	if err != nil {
		t.Fatalf("seed config: %v", err)
	}

	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{
		"hindsight": &memoryHandlerFakeProvider{name: "hindsight", recallResults: json.RawMessage(`[{"id":"primary-only"}]`)},
		"mem0":      &memoryHandlerFakeProvider{name: "mem0", recallResults: json.RawMessage(`[{"id":"shadow-only"}]`)},
	}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	w := httptest.NewRecorder()
	testHandler.CreateMemoryRecall(w, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/recall", map[string]any{
		"read_mode":      "dual",
		"correlation_id": "handler-pair-1",
		"query":          "compare scoped memory",
		"limit":          5,
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("CreateMemoryRecall: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res MemoryRecallResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Mode != "dual" || res.RecallCorrelationID != "handler-pair-1" {
		t.Fatalf("mode/correlation = %q/%q, want dual/handler-pair-1", res.Mode, res.RecallCorrelationID)
	}
	if res.Primary == nil || res.Shadow == nil {
		t.Fatalf("primary/shadow response = %#v/%#v, want both", res.Primary, res.Shadow)
	}
	if res.Primary.Results[0].(map[string]any)["id"] != "primary-only" || res.Shadow.Results[0].(map[string]any)["id"] != "shadow-only" {
		t.Fatalf("provider results were not separate: primary=%#v shadow=%#v", res.Primary.Results, res.Shadow.Results)
	}

	var sampleCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM memory_recall_sample
		WHERE workspace_id = $1 AND recall_correlation_id = 'handler-pair-1' AND read_mode = 'dual'
	`, testWorkspaceID).Scan(&sampleCount); err != nil {
		t.Fatalf("count recall samples: %v", err)
	}
	if sampleCount != 2 {
		t.Fatalf("paired recall samples = %d, want 2", sampleCount)
	}
}

func TestGetMemoryMem0BoardReturnsRealDeliveriesAndRecallSamples(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "mem0", "")

	provider := &memoryHandlerFakeProvider{name: "mem0", recallResults: json.RawMessage(`[{"id":"recall-memory"}]`)}
	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{"mem0": provider}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "board-retain",
		Content:        json.RawMessage(`{"text":"remember me"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	worker := NewMemoryDeliveryWorker(testHandler)
	worked, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch due delivery")
	}

	recall := httptest.NewRecorder()
	testHandler.CreateMemoryRecall(recall, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/recall", map[string]any{
		"provider":       "mem0",
		"correlation_id": "board-recall",
		"query":          "remember",
	}))
	if recall.Code != http.StatusOK {
		t.Fatalf("CreateMemoryRecall: expected 200, got %d: %s", recall.Code, recall.Body.String())
	}

	w := httptest.NewRecorder()
	testHandler.GetMemoryMem0Board(w, memoryHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory/mem0-board", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemoryMem0Board: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res MemoryMem0BoardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Health == nil || !res.Health.OK || res.Health.Provider != "mem0" {
		t.Fatalf("health = %#v, want ok mem0", res.Health)
	}
	if len(res.Deliveries) != 1 {
		t.Fatalf("deliveries = %#v, want one mem0 delivery", res.Deliveries)
	}
	delivery := res.Deliveries[0]
	if delivery.MemoryEventID != uuidToString(retain.Event.ID) || delivery.EventType != "retain" || delivery.Status != "delivered" {
		t.Fatalf("delivery = %#v, want delivered retain for event", delivery)
	}
	if len(res.RecallSamples) != 1 || res.RecallSamples[0].RecallCorrelationID != "board-recall" {
		t.Fatalf("recall samples = %#v, want board-recall sample", res.RecallSamples)
	}
}

func TestCreateMemoryRecallHonorsReleaseFlagRollback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	w := httptest.NewRecorder()
	testHandler.CreateMemoryRecall(w, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/recall", map[string]any{
		"read_mode": "primary",
		"query":     "should not run",
	}))
	if w.Code != http.StatusNotFound {
		t.Fatalf("CreateMemoryRecall flag off: expected 404, got %d: %s", w.Code, w.Body.String())
	}
	var sampleCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM memory_recall_sample WHERE workspace_id = $1`, testWorkspaceID).Scan(&sampleCount); err != nil {
		t.Fatalf("count recall samples: %v", err)
	}
	if sampleCount != 0 {
		t.Fatalf("recall samples with flag off = %d, want 0", sampleCount)
	}
}

type memoryHandlerFakeProvider struct {
	name          string
	recallResults json.RawMessage
	retained      []service.MemoryEventEnvelope
}

func (p *memoryHandlerFakeProvider) Name() string { return p.name }

func (p *memoryHandlerFakeProvider) Retain(_ context.Context, event service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	p.retained = append(p.retained, event)
	return service.MemoryProviderResult{ProviderMemoryID: "handler-memory-1", Response: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *memoryHandlerFakeProvider) Recall(_ context.Context, req service.MemoryRecallRequest) (service.MemoryRecallResult, error) {
	return service.MemoryRecallResult{Provider: req.Provider, Results: p.recallResults, Provenance: json.RawMessage(`{"handler":"test"}`)}, nil
}

func (p *memoryHandlerFakeProvider) Update(context.Context, service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	return service.MemoryProviderResult{}, nil
}

func (p *memoryHandlerFakeProvider) Invalidate(context.Context, service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	return service.MemoryProviderResult{}, nil
}

func (p *memoryHandlerFakeProvider) Delete(context.Context, service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	return service.MemoryProviderResult{}, nil
}

func (p *memoryHandlerFakeProvider) Health(context.Context) (service.MemoryProviderHealth, error) {
	return service.MemoryProviderHealth{Provider: p.name, OK: true}, nil
}

func TestMemoryDeliveryWorkerHonorsReleaseFlagRollback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	enableMemoryConfigForTest(t, testWorkspaceID, "hindsight", "")

	provider := &memoryHandlerFakeProvider{name: "hindsight"}
	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{"hindsight": provider}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	if _, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "worker-flag-off",
		Content:        json.RawMessage(`{"text":"do not dispatch"}`),
	}); err != nil {
		t.Fatalf("retain: %v", err)
	}

	worker := NewMemoryDeliveryWorker(testHandler)
	worked, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if worked {
		t.Fatal("worker reported work while memory gateway flag was disabled")
	}
	if len(provider.retained) != 0 {
		t.Fatalf("provider retain calls = %d, want 0", len(provider.retained))
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status FROM memory_provider_delivery WHERE workspace_id = $1 AND provider = 'hindsight'
	`, testWorkspaceID).Scan(&status); err != nil {
		t.Fatalf("read delivery status: %v", err)
	}
	if status != "queued" {
		t.Fatalf("delivery status = %q, want queued", status)
	}
}

func TestMemoryDeliveryWorkerDispatchesDueProviderDelivery(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "hindsight", "")

	provider := &memoryHandlerFakeProvider{name: "hindsight"}
	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{"hindsight": provider}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "worker-dispatches-due",
		Content:        json.RawMessage(`{"text":"dispatch me"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}

	worker := NewMemoryDeliveryWorker(testHandler)
	worked, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch due delivery")
	}
	if len(provider.retained) != 1 {
		t.Fatalf("provider retain calls = %d, want 1", len(provider.retained))
	}
	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status FROM memory_provider_delivery WHERE workspace_id = $1 AND memory_event_id = $2 AND provider = 'hindsight'
	`, testWorkspaceID, retain.Event.ID).Scan(&status); err != nil {
		t.Fatalf("read delivery status: %v", err)
	}
	if status != "delivered" {
		t.Fatalf("delivery status = %q, want delivered", status)
	}
}

func TestMemoryEventReadIsWorkspaceScoped(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	otherWorkspaceID := createOtherTestWorkspace(t)
	cleanupMemoryGatewayTestRows(t, testWorkspaceID, otherWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID, otherWorkspaceID) })
	enableMemoryConfigForTest(t, otherWorkspaceID, "mem0", "")

	res, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(otherWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "foreign-memory-key",
		Content:        json.RawMessage(`{"fact":"foreign workspace memory"}`),
	})
	if err != nil {
		t.Fatalf("retain foreign memory event: %v", err)
	}
	_, err = testHandler.Queries.GetMemoryEventInWorkspace(context.Background(), db.GetMemoryEventInWorkspaceParams{
		ID:          res.Event.ID,
		WorkspaceID: parseUUID(testWorkspaceID),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-workspace GetMemoryEventInWorkspace error = %v, want pgx.ErrNoRows", err)
	}
}
