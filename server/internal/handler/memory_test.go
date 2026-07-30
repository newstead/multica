package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/featureflags"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func memoryHandlerRequest(method, path string, body any) *http.Request {
	req := newRequest(method, path, body)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testWorkspaceID)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func memoryHandlerEventRequest(method, path, eventID string, body any) *http.Request {
	req := memoryHandlerRequest(method, path, body)
	rctx := chi.RouteContext(req.Context())
	rctx.URLParams.Add("eventId", eventID)
	return req
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

func TestMemoryAdminRoutesRequireWorkspaceAdmin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	memberID := createPermissionTestMember(t, "memory-admin-route-member@multica.test")
	eventID := "11111111-2222-3333-4444-555555555555"

	router := chi.NewRouter()
	router.Route("/api/workspaces/{id}", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceRoleFromURL(testHandler.Queries, "id", "owner", "admin"))
			r.Post("/memory/audit/{eventId}/correct", testHandler.CorrectMemoryAuditEvent)
			r.Post("/memory/audit/{eventId}/invalidate", testHandler.InvalidateMemoryAuditEvent)
			r.Delete("/memory/audit/{eventId}", testHandler.DeleteMemoryAuditEvent)
			r.Post("/memory/erase", testHandler.EraseMemoryScope)
		})
	})

	exercise := func(method, path, userID string, body any) int {
		req := newRequestAs(userID, method, path, body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	paths := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/workspaces/" + testWorkspaceID + "/memory/audit/" + eventID + "/correct", map[string]any{"text": "corrected"}},
		{http.MethodPost, "/api/workspaces/" + testWorkspaceID + "/memory/audit/" + eventID + "/invalidate", map[string]any{"confirmation": "INVALIDATE"}},
		{http.MethodDelete, "/api/workspaces/" + testWorkspaceID + "/memory/audit/" + eventID, map[string]any{"confirmation": "DELETE"}},
		{http.MethodPost, "/api/workspaces/" + testWorkspaceID + "/memory/erase", map[string]any{"scope": "workspace", "confirmation": "ERASE"}},
	}
	for _, tc := range paths {
		if code := exercise(tc.method, tc.path, memberID, tc.body); code != http.StatusForbidden {
			t.Fatalf("member %s %s: got %d, want 403", tc.method, tc.path, code)
		}
		if code := exercise(tc.method, tc.path, testUserID, tc.body); code == http.StatusForbidden {
			t.Fatalf("owner %s %s: got unexpected 403", tc.method, tc.path)
		}
	}
}

func TestDeleteMemoryAuditEventRequiresConfirmation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	w := httptest.NewRecorder()
	testHandler.DeleteMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/memory/audit/11111111-2222-3333-4444-555555555555", "11111111-2222-3333-4444-555555555555", map[string]any{"confirmation": ""}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DeleteMemoryAuditEvent missing confirmation: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delete confirmation is required") {
		t.Fatalf("DeleteMemoryAuditEvent missing confirmation body = %s", w.Body.String())
	}
}

func TestDeleteMemoryAuditEventReportsProviderPartialFailure(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "mem0", "hindsight")

	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{
		"mem0":      &memoryHandlerFakeProvider{name: "mem0"},
		"hindsight": &memoryHandlerFakeProvider{name: "hindsight", deleteErr: errors.New("provider rejected delete")},
	}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "handler-delete-partial",
		Content:        json.RawMessage(`{"text":"delete partial"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	for _, delivery := range retain.Deliveries {
		if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
			t.Fatalf("dispatch retain delivery: %v", err)
		}
	}

	w := httptest.NewRecorder()
	testHandler.DeleteMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/memory/audit/"+uuidToString(retain.Event.ID), uuidToString(retain.Event.ID), map[string]any{"confirmation": "DELETE"}))
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteMemoryAuditEvent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res MemoryMutationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Operation != "delete" || len(res.Results) != 2 {
		t.Fatalf("delete response = %#v, want two provider results", res)
	}
	byProvider := map[string]MemoryMutationProviderResult{}
	for _, result := range res.Results {
		byProvider[result.Provider] = result
	}
	if byProvider["mem0"].Status != "delivered" || byProvider["mem0"].ProviderMemoryID == "" {
		t.Fatalf("mem0 result = %#v, want delivered with provider memory id", byProvider["mem0"])
	}
	if byProvider["hindsight"].Error != "provider rejected delete" || byProvider["hindsight"].ProviderMemoryID == "" || byProvider["hindsight"].Status == "delivered" {
		t.Fatalf("hindsight result = %#v, want failed provider result with memory id and error", byProvider["hindsight"])
	}
}

func TestEraseMemoryScopeTargetsProjectOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "mem0", "")

	provider := &memoryHandlerFakeProvider{name: "mem0"}
	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{"mem0": provider}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	projectA := parseUUID("11111111-2222-3333-4444-555555555555")
	projectB := parseUUID("22222222-3333-4444-5555-666666666666")
	fixtures := []struct {
		key       string
		projectID pgtype.UUID
	}{
		{key: "erase-project-a", projectID: projectA},
		{key: "erase-project-b", projectID: projectB},
	}
	for _, fixture := range fixtures {
		retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
			Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID), ProjectID: fixture.projectID},
			Actor:          service.MemoryActor{Type: "system"},
			EventType:      "retain",
			IdempotencyKey: fixture.key,
			Content:        json.RawMessage(`{"text":"scope erase"}`),
		})
		if err != nil {
			t.Fatalf("retain %s: %v", fixture.key, err)
		}
		for _, delivery := range retain.Deliveries {
			if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
				t.Fatalf("dispatch retain delivery %s: %v", fixture.key, err)
			}
		}
	}

	w := httptest.NewRecorder()
	testHandler.EraseMemoryScope(w, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/erase", map[string]any{"scope": "project", "project_id": uuidToString(projectA), "confirmation": "ERASE"}))
	if w.Code != http.StatusOK {
		t.Fatalf("EraseMemoryScope: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res MemoryMutationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Operation != "erase" || len(res.Results) != 1 {
		t.Fatalf("erase response = %#v, want one project-scoped provider result", res)
	}
	if res.Results[0].Provider != "mem0" || res.Results[0].Status != "delivered" || res.Results[0].ProviderMemoryID != "mem0-memory-1" {
		t.Fatalf("erase result = %#v, want project A mem0 memory only", res.Results[0])
	}
}

func TestDeleteMemoryAuditEventRejectsMismatchedTargetID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	cleanupMemoryGatewayTestRows(t, testWorkspaceID)
	t.Cleanup(func() { cleanupMemoryGatewayTestRows(t, testWorkspaceID) })
	withFeatureFlag(t, testHandler, featureflags.MemoryGateway, true)
	enableMemoryConfigForTest(t, testWorkspaceID, "mem0", "")

	provider := &memoryHandlerFakeProvider{name: "mem0"}
	oldProviders := testHandler.MemoryService.Providers
	testHandler.MemoryService.Providers = map[string]service.MemoryProvider{"mem0": provider}
	t.Cleanup(func() { testHandler.MemoryService.Providers = oldProviders })

	retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "handler-mismatched-delete-target",
		Content:        json.RawMessage(`{"text":"delete target"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	for _, delivery := range retain.Deliveries {
		if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
			t.Fatalf("dispatch retain delivery: %v", err)
		}
	}

	w := httptest.NewRecorder()
	testHandler.DeleteMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/memory/audit/"+uuidToString(retain.Event.ID), uuidToString(retain.Event.ID), map[string]any{
		"provider":           "mem0",
		"provider_memory_id": "different-memory",
		"confirmation":       "DELETE",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("DeleteMemoryAuditEvent mismatched target: expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "provider_memory_id does not match") {
		t.Fatalf("mismatched target body = %s", w.Body.String())
	}
	if len(provider.deleted) != 0 {
		t.Fatalf("provider delete calls = %d, want 0", len(provider.deleted))
	}
	var deleteEvents int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM memory_event WHERE workspace_id = $1 AND event_type = 'delete'`, testWorkspaceID).Scan(&deleteEvents); err != nil {
		t.Fatalf("count delete audit events: %v", err)
	}
	if deleteEvents != 0 {
		t.Fatalf("delete audit events = %d, want 0", deleteEvents)
	}
}

func TestCorrectHindsightDocumentUsesCanonicalDocumentTarget(t *testing.T) {
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
		IdempotencyKey: "handler-hindsight-document-correct",
		Content:        json.RawMessage(`{"text":"document target"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	for _, delivery := range retain.Deliveries {
		if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
			t.Fatalf("dispatch retain delivery: %v", err)
		}
	}

	w := httptest.NewRecorder()
	testHandler.CorrectMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/audit/"+uuidToString(retain.Event.ID)+"/correct", uuidToString(retain.Event.ID), map[string]any{
		"provider": "hindsight",
		"text":     "corrected document text",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("CorrectMemoryAuditEvent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(provider.updated) != 1 {
		t.Fatalf("provider update calls = %d, want 1", len(provider.updated))
	}
	content := memoryHandlerFakeEventContent(t, provider.updated[0])
	if content["document_id"] != "hindsight-memory-1" {
		t.Fatalf("hindsight correction document_id = %#v, want canonical delivery target", content["document_id"])
	}
	if content["memory_id"] != nil || content["provider_memory_id"] != nil {
		t.Fatalf("hindsight document correction included memory identifiers: %#v", content)
	}
}

func TestInvalidateHindsightDocumentTargetSkipsWithoutVerifiedMemoryID(t *testing.T) {
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
		IdempotencyKey: "handler-hindsight-document-invalidate-skip",
		Content:        json.RawMessage(`{"text":"document only"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	for _, delivery := range retain.Deliveries {
		if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
			t.Fatalf("dispatch retain delivery: %v", err)
		}
	}

	w := httptest.NewRecorder()
	testHandler.InvalidateMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/audit/"+uuidToString(retain.Event.ID)+"/invalidate", uuidToString(retain.Event.ID), map[string]any{
		"provider":     "hindsight",
		"confirmation": "INVALIDATE",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("InvalidateMemoryAuditEvent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var res MemoryMutationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].Status != "skipped" || !strings.Contains(res.Results[0].Error, "verified provider memory id is missing") {
		t.Fatalf("invalidate response = %#v, want skipped missing verified memory id", res)
	}
	if len(provider.invalidated) != 0 {
		t.Fatalf("provider invalidate calls = %d, want 0", len(provider.invalidated))
	}
}

func TestInvalidateHindsightMemoryUsesCanonicalMemoryTarget(t *testing.T) {
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

	updateEvent, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "update",
		IdempotencyKey: "handler-hindsight-memory-invalidate",
		Content:        json.RawMessage(`{"memory_id":"fact-1","text":"corrected fact"}`),
	})
	if err != nil {
		t.Fatalf("seed update event: %v", err)
	}
	for _, delivery := range updateEvent.Deliveries {
		if _, err := testHandler.MemoryService.DispatchMemoryProviderDelivery(context.Background(), parseUUID(testWorkspaceID), delivery.ID); err != nil {
			t.Fatalf("dispatch update delivery: %v", err)
		}
	}
	provider.updated = nil

	w := httptest.NewRecorder()
	testHandler.InvalidateMemoryAuditEvent(w, memoryHandlerEventRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/audit/"+uuidToString(updateEvent.Event.ID)+"/invalidate", uuidToString(updateEvent.Event.ID), map[string]any{
		"provider":     "hindsight",
		"confirmation": "INVALIDATE",
	}))
	if w.Code != http.StatusOK {
		t.Fatalf("InvalidateMemoryAuditEvent: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(provider.invalidated) != 1 {
		t.Fatalf("provider invalidate calls = %d, want 1", len(provider.invalidated))
	}
	content := memoryHandlerFakeEventContent(t, provider.invalidated[0])
	if content["memory_id"] != "fact-1" || content["document_id"] != nil {
		t.Fatalf("hindsight invalidation content = %#v, want memory_id fact-1 only", content)
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

	projectID := "11111111-2222-3333-4444-555555555555"
	otherProjectID := "22222222-3333-4444-5555-666666666666"
	retain, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID), ProjectID: parseUUID(projectID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "board-retain",
		Content:        json.RawMessage(`{"text":"remember me"}`),
	})
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if _, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID), ProjectID: parseUUID(otherProjectID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "retain",
		IdempotencyKey: "board-retain-other-project",
		Content:        json.RawMessage(`{"text":"do not leak me"}`),
	}); err != nil {
		t.Fatalf("retain other project: %v", err)
	}
	worker := NewMemoryDeliveryWorker(testHandler)
	worked, err := worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch due delivery")
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext other project: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch other project delivery")
	}

	historyOK, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID), ProjectID: parseUUID(projectID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "history",
		Provider:       service.Mem0ProviderName,
		IdempotencyKey: "board-history-ok",
		Content:        json.RawMessage(`{"memory_id":"handler-memory-1"}`),
	})
	if err != nil {
		t.Fatalf("history ok event: %v", err)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext history ok: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch history ok delivery")
	}
	provider.historyErr = errors.New("history unavailable raw provider detail")
	historyFailed, err := testHandler.MemoryService.Retain(context.Background(), service.MemoryRetainRequest{
		Scope:          service.MemoryScope{WorkspaceID: parseUUID(testWorkspaceID), ProjectID: parseUUID(projectID)},
		Actor:          service.MemoryActor{Type: "system"},
		EventType:      "history",
		Provider:       service.Mem0ProviderName,
		IdempotencyKey: "board-history-failed",
		Content:        json.RawMessage(`{"memory_id":"handler-memory-1"}`),
	})
	if err != nil {
		t.Fatalf("history failed event: %v", err)
	}
	worked, err = worker.ProcessNext(context.Background())
	if err != nil {
		t.Fatalf("ProcessNext history failed: %v", err)
	}
	if !worked {
		t.Fatal("worker did not dispatch history failed delivery")
	}

	recall := httptest.NewRecorder()
	testHandler.CreateMemoryRecall(recall, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/recall", map[string]any{
		"provider":       "mem0",
		"project_id":     projectID,
		"correlation_id": "board-recall",
		"query":          "remember",
	}))
	if recall.Code != http.StatusOK {
		t.Fatalf("CreateMemoryRecall: expected 200, got %d: %s", recall.Code, recall.Body.String())
	}
	foreignRecall := httptest.NewRecorder()
	testHandler.CreateMemoryRecall(foreignRecall, memoryHandlerRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory/recall", map[string]any{
		"provider":       "mem0",
		"project_id":     otherProjectID,
		"correlation_id": "board-recall-other-project",
		"query":          "remember",
	}))
	if foreignRecall.Code != http.StatusOK {
		t.Fatalf("CreateMemoryRecall other project: expected 200, got %d: %s", foreignRecall.Code, foreignRecall.Body.String())
	}

	w := httptest.NewRecorder()
	testHandler.GetMemoryMem0Board(w, memoryHandlerRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory/mem0-board?project_id="+projectID, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetMemoryMem0Board: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "provider_memory_id") || strings.Contains(body, "response") || strings.Contains(body, "handler-memory-1") || strings.Contains(body, "history unavailable raw provider detail") || strings.Contains(body, "do not leak me") {
		t.Fatalf("GetMemoryMem0Board leaked provider/raw data: %s", body)
	}
	var res MemoryMem0BoardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if res.Health == nil || !res.Health.OK || res.Health.Provider != "mem0" {
		t.Fatalf("health = %#v, want ok mem0", res.Health)
	}
	if len(res.Deliveries) != 3 {
		t.Fatalf("deliveries = %#v, want retain plus history success/failure", res.Deliveries)
	}
	deliveriesByEvent := make(map[string]MemoryMem0BoardDeliveryResponse, len(res.Deliveries))
	for _, delivery := range res.Deliveries {
		deliveriesByEvent[delivery.MemoryEventID] = delivery
	}
	delivery := deliveriesByEvent[uuidToString(retain.Event.ID)]
	if delivery.EventType != "retain" || delivery.Status != "delivered" {
		t.Fatalf("retain delivery = %#v, want delivered retain for event", delivery)
	}
	historyOKDelivery := deliveriesByEvent[uuidToString(historyOK.Event.ID)]
	if historyOKDelivery.EventType != "history" || historyOKDelivery.Status != "delivered" {
		t.Fatalf("history ok delivery = %#v, want delivered history", historyOKDelivery)
	}
	historyFailedDelivery := deliveriesByEvent[uuidToString(historyFailed.Event.ID)]
	if historyFailedDelivery.EventType != "history" || historyFailedDelivery.Status != "retry" {
		t.Fatalf("history failed delivery = %#v, want retry history failure", historyFailedDelivery)
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
	historyErr    error
	retained      []service.MemoryEventEnvelope
	history       []string
	updated       []service.MemoryEventEnvelope
	invalidated   []service.MemoryEventEnvelope
	deleted       []service.MemoryEventEnvelope
	deleteErr     error
}

func (p *memoryHandlerFakeProvider) Name() string { return p.name }

func (p *memoryHandlerFakeProvider) Retain(_ context.Context, event service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	p.retained = append(p.retained, event)
	return service.MemoryProviderResult{ProviderMemoryID: fmt.Sprintf("%s-memory-%d", p.name, len(p.retained)), Response: json.RawMessage(`{"ok":true}`)}, nil
}

func (p *memoryHandlerFakeProvider) Recall(_ context.Context, req service.MemoryRecallRequest) (service.MemoryRecallResult, error) {
	return service.MemoryRecallResult{Provider: req.Provider, Results: p.recallResults, Provenance: json.RawMessage(`{"handler":"test"}`)}, nil
}

func (p *memoryHandlerFakeProvider) Update(_ context.Context, event service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	p.updated = append(p.updated, event)
	targetID := memoryHandlerFakeEventTargetID(event)
	return service.MemoryProviderResult{ProviderMemoryID: targetID, Response: json.RawMessage(fmt.Sprintf(`{"id":%q,"ok":true}`, targetID))}, nil
}

func (p *memoryHandlerFakeProvider) Invalidate(_ context.Context, event service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	p.invalidated = append(p.invalidated, event)
	targetID := memoryHandlerFakeEventTargetID(event)
	return service.MemoryProviderResult{ProviderMemoryID: targetID, Response: json.RawMessage(fmt.Sprintf(`{"id":%q,"ok":true}`, targetID))}, nil
}

func (p *memoryHandlerFakeProvider) Delete(_ context.Context, event service.MemoryEventEnvelope) (service.MemoryProviderResult, error) {
	p.deleted = append(p.deleted, event)
	if p.deleteErr != nil {
		return service.MemoryProviderResult{}, p.deleteErr
	}
	targetID := memoryHandlerFakeEventTargetID(event)
	return service.MemoryProviderResult{ProviderMemoryID: targetID, Response: json.RawMessage(fmt.Sprintf(`{"id":%q,"ok":true}`, targetID))}, nil
}

func (p *memoryHandlerFakeProvider) History(_ context.Context, _ service.MemoryScope, memoryID string) (json.RawMessage, error) {
	p.history = append(p.history, memoryID)
	if p.historyErr != nil {
		return nil, p.historyErr
	}
	return json.RawMessage(`{"events":[{"operation":"UPDATE","redacted":true}]}`), nil
}

func (p *memoryHandlerFakeProvider) Health(context.Context) (service.MemoryProviderHealth, error) {
	return service.MemoryProviderHealth{Provider: p.name, OK: true}, nil
}

func memoryHandlerFakeEventContent(t *testing.T, event service.MemoryEventEnvelope) map[string]any {
	t.Helper()
	var content map[string]any
	if err := json.Unmarshal(event.Content, &content); err != nil {
		t.Fatalf("decode fake provider event content: %v", err)
	}
	return content
}

func memoryHandlerFakeEventTargetID(event service.MemoryEventEnvelope) string {
	var content map[string]any
	_ = json.Unmarshal(event.Content, &content)
	for _, key := range []string{"memory_id", "document_id", "provider_memory_id"} {
		if value, ok := content[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
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
