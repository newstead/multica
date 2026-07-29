package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
