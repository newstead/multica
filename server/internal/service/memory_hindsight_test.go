package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
)

const (
	hindsightTestWorkspaceA = "11111111-1111-1111-1111-111111111111"
	hindsightTestWorkspaceB = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	hindsightTestProject    = "22222222-2222-2222-2222-222222222222"
	hindsightTestAgent      = "33333333-3333-3333-3333-333333333333"
	hindsightTestIssue      = "44444444-4444-4444-4444-444444444444"
	hindsightTestTask       = "55555555-5555-5555-5555-555555555555"
)

func TestHindsightRetainContractUsesWorkspaceBankStrictTagsAndIdempotency(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		operationIDs []string
		documentIDs  []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		wantPath := "/v1/default/banks/multica-ws-11111111111111111111111111111111/memories"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("Authorization = %q", got)
		}

		var request struct {
			Items []struct {
				Content           string            `json:"content"`
				Metadata          map[string]string `json:"metadata"`
				DocumentID        string            `json:"document_id"`
				Tags              []string          `json:"tags"`
				ObservationScopes string            `json:"observation_scopes"`
				UpdateMode        string            `json:"update_mode"`
			} `json:"items"`
			Async       bool   `json:"async"`
			OperationID string `json:"operation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if !request.Async {
			t.Error("retain must use async=true so caller operation_id is idempotent")
		}
		if len(request.Items) != 1 {
			t.Errorf("items = %d, want 1", len(request.Items))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		item := request.Items[0]
		wantTags := hindsightTestScopeTags()
		if !sameStringSet(item.Tags, wantTags) {
			t.Errorf("tags = %#v, want %#v", item.Tags, wantTags)
		}
		if item.ObservationScopes != "combined" {
			t.Errorf("observation_scopes = %q, want combined", item.ObservationScopes)
		}
		if item.UpdateMode != "replace" {
			t.Errorf("update_mode = %q, want replace", item.UpdateMode)
		}
		if item.Metadata["workspace_id"] != hindsightTestWorkspaceA ||
			item.Metadata["project_id"] != hindsightTestProject ||
			item.Metadata["agent_id"] != hindsightTestAgent ||
			item.Metadata["issue_id"] != hindsightTestIssue ||
			item.Metadata["task_id"] != hindsightTestTask {
			t.Errorf("scope metadata = %#v", item.Metadata)
		}
		if item.Metadata["credential"] != "not-a-secret-field" {
			t.Errorf("caller metadata was not preserved: %#v", item.Metadata)
		}

		mu.Lock()
		operationIDs = append(operationIDs, request.OperationID)
		documentIDs = append(documentIDs, item.DocumentID)
		mu.Unlock()
		writeHindsightJSON(t, w, map[string]any{
			"success":      true,
			"bank_id":      "ignored",
			"items_count":  1,
			"async":        true,
			"operation_id": request.OperationID,
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{APIKey: "test-secret"})
	event := hindsightTestEvent(`{"text":"remember this"}`)
	event.Metadata = map[string]any{
		"source_type": "issue_comment",
		"source_id":   "66666666-6666-6666-6666-666666666666",
		"credential":  "not-a-secret-field",
	}

	first, err := provider.Retain(context.Background(), event)
	if err != nil {
		t.Fatalf("Retain first: %v", err)
	}
	second, err := provider.Retain(context.Background(), event)
	if err != nil {
		t.Fatalf("Retain second: %v", err)
	}
	if first.ProviderMemoryID == "" || first.ProviderMemoryID != second.ProviderMemoryID {
		t.Fatalf("provider document IDs = %q, %q; want same non-empty ID", first.ProviderMemoryID, second.ProviderMemoryID)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(operationIDs) != 2 || operationIDs[0] == "" || operationIDs[0] != operationIDs[1] {
		t.Fatalf("operation IDs = %#v, want same non-empty caller UUID", operationIDs)
	}
	if len(documentIDs) != 2 || documentIDs[0] == "" || documentIDs[0] != documentIDs[1] {
		t.Fatalf("document IDs = %#v, want same non-empty ID", documentIDs)
	}
}

func TestHindsightAggregateTelemetryUsesOnlyConfiguredPrivatePath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/aggregate" || r.Method != http.MethodGet {
			t.Fatalf("aggregate request = %s %s, want GET /aggregate", r.Method, r.URL.Path)
		}
		writeHindsightJSON(t, w, map[string]any{"storage_bytes": 1024, "variable_cost_usd_total": 0.25})
	}))
	defer server.Close()
	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{AggregateTelemetryPath: "/aggregate"})
	measurement, err := provider.AggregateTelemetry(context.Background())
	if err != nil || measurement.StorageBytes != 1024 || measurement.VariableCostUSDTotal != 0.25 {
		t.Fatalf("aggregate measurement = %+v, %v", measurement, err)
	}
}

func TestHindsightRetainAcceptsProviderGeneratedOperationID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Items []struct {
				DocumentID string `json:"document_id"`
			} `json:"items"`
			OperationID string `json:"operation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.OperationID == "" {
			t.Error("request operation_id is empty")
		}
		if len(request.Items) != 1 || request.Items[0].DocumentID == "" {
			t.Errorf("document_id missing: %#v", request.Items)
		}
		writeHindsightJSON(t, w, map[string]any{
			"success":      true,
			"operation_id": "provider-generated-operation",
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{})
	result, err := provider.Retain(context.Background(), hindsightTestEvent(`{"text":"remember this"}`))
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if result.ProviderMemoryID == "" {
		t.Fatal("ProviderMemoryID is empty")
	}
}

func TestHindsightRecallEnforcesWorkspaceIsolationAndProvenance(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/default/banks/multica-ws-11111111111111111111111111111111/memories/recall"
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		var request hindsightRecallRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode recall request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.TagsMatch != "exact" {
			t.Errorf("tags_match = %q, want exact", request.TagsMatch)
		}
		if request.MaxTokens != maxHindsightRecallTokens {
			t.Errorf("max_tokens = %d, want hard cap %d", request.MaxTokens, maxHindsightRecallTokens)
		}
		wantTags := hindsightTestScopeTags()
		if !sameStringSet(request.Tags, wantTags) {
			t.Errorf("tags = %#v, want %#v", request.Tags, wantTags)
		}

		writeHindsightJSON(t, w, map[string]any{
			"results": []any{
				map[string]any{
					"id":          "fact-ok",
					"text":        "correctly scoped",
					"document_id": "doc-ok",
					"tags":        wantTags,
					"scores":      map[string]any{"final": 0.82},
				},
				map[string]any{
					"id":   "fact-other-workspace",
					"text": "must not escape the adapter",
					"tags": []string{
						"workspace:" + hindsightTestWorkspaceB,
						"project:" + hindsightTestProject,
						"agent:" + hindsightTestAgent,
						"issue:" + hindsightTestIssue,
					},
					"scores": map[string]any{"final": 0.99},
				},
				map[string]any{
					"id":     "fact-broader-scope",
					"text":   "extra tags must not match exact scope",
					"tags":   append(append([]string{}, wantTags...), "issue:77777777-7777-7777-7777-777777777777"),
					"scores": map[string]any{"final": 0.95},
				},
			},
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{RecallMaxTokens: 5000})
	result, err := provider.Recall(context.Background(), MemoryRecallRequest{
		Scope:    hindsightTestMemoryScope(),
		Provider: "hindsight",
		Query:    "What is relevant?",
		Limit:    5000,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	var results []map[string]any
	if err := json.Unmarshal(result.Results, &results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if len(results) != 1 || results[0]["id"] != "fact-ok" {
		t.Fatalf("filtered results = %#v, want only fact-ok", results)
	}

	var provenance hindsightRecallProvenance
	if err := json.Unmarshal(result.Provenance, &provenance); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	if provenance.Provider != "hindsight" || provenance.TagsMatch != "exact" || provenance.MaxTokens != maxHindsightRecallTokens {
		t.Errorf("provenance = %#v", provenance)
	}
	if len(provenance.Items) != 1 ||
		provenance.Items[0].ProviderRecordID != "fact-ok" ||
		provenance.Items[0].Score == nil ||
		*provenance.Items[0].Score != 0.82 {
		t.Errorf("provenance items = %#v", provenance.Items)
	}
}

func TestHindsightCorrectionInvalidationRestoreAndDeleteLifecycle(t *testing.T) {
	t.Parallel()

	type recordedRequest struct {
		Method string
		Path   string
		Body   map[string]any
	}
	var (
		mu       sync.Mutex
		recorded []recordedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		mu.Lock()
		recorded = append(recorded, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
		mu.Unlock()

		if r.Method == http.MethodDelete {
			writeHindsightJSON(t, w, map[string]any{
				"success":              true,
				"message":              "deleted",
				"document_id":          "doc-1",
				"memory_units_deleted": 1,
			})
			return
		}
		writeHindsightJSON(t, w, map[string]any{
			"id":    "fact-1",
			"text":  "updated",
			"state": body["state"],
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{})
	event := hindsightTestEvent(`{"memory_id":"fact-1","text":"corrected fact","entities":[]}`)
	if _, err := provider.Update(context.Background(), event); err != nil {
		t.Fatalf("Update: %v", err)
	}
	event.Content = json.RawMessage(`{"memory_id":"fact-1","reason":"superseded"}`)
	if _, err := provider.Invalidate(context.Background(), event); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := provider.Restore(context.Background(), event); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	event.Content = json.RawMessage(`{"document_id":"doc-1"}`)
	if _, err := provider.Delete(context.Background(), event); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 4 {
		t.Fatalf("recorded %d requests, want 4: %#v", len(recorded), recorded)
	}
	memoryPath := "/v1/default/banks/multica-ws-11111111111111111111111111111111/memories/fact-1"
	if recorded[0].Method != http.MethodPatch || recorded[0].Path != memoryPath || recorded[0].Body["text"] != "corrected fact" {
		t.Errorf("correction request = %#v", recorded[0])
	}
	entities, ok := recorded[0].Body["entities"].([]any)
	if !ok || len(entities) != 0 {
		t.Errorf("correction entities = %#v, want explicit empty array", recorded[0].Body["entities"])
	}
	if recorded[1].Method != http.MethodPatch || recorded[1].Path != memoryPath || recorded[1].Body["state"] != "invalidated" {
		t.Errorf("invalidation request = %#v", recorded[1])
	}
	if recorded[2].Method != http.MethodPatch || recorded[2].Path != memoryPath || recorded[2].Body["state"] != "valid" {
		t.Errorf("restore request = %#v", recorded[2])
	}
	deletePath := "/v1/default/banks/multica-ws-11111111111111111111111111111111/documents/doc-1"
	if recorded[3].Method != http.MethodDelete || recorded[3].Path != deletePath {
		t.Errorf("delete request = %#v, want DELETE %s", recorded[3], deletePath)
	}
	for _, request := range recorded {
		if strings.Contains(request.Path, "/reflect") {
			t.Fatalf("normal adapter lifecycle called reflect: %#v", request)
		}
	}
}

func TestHindsightDocumentUpdateUsesDocumentRetainTarget(t *testing.T) {
	t.Parallel()

	type recordedRequest struct {
		Method string
		Path   string
		Body   map[string]any
	}
	var recorded []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		recorded = append(recorded, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
		writeHindsightJSON(t, w, map[string]any{
			"success":      true,
			"operation_id": body["operation_id"],
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{})
	event := hindsightTestEvent(`{"document_id":"doc-canonical","text":"correct document"}`)
	result, err := provider.Update(context.Background(), event)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.ProviderMemoryID != "doc-canonical" {
		t.Fatalf("ProviderMemoryID = %q, want document target", result.ProviderMemoryID)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(recorded))
	}
	request := recorded[0]
	if request.Method != http.MethodPost || !strings.HasSuffix(request.Path, "/memories") {
		t.Fatalf("document correction request = %#v, want POST memories retain path", request)
	}
	items, ok := request.Body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %#v, want one document item", request.Body["items"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["document_id"] != "doc-canonical" {
		t.Fatalf("document item = %#v, want canonical document_id", items[0])
	}
}

func TestHindsightInvalidateDoesNotTreatProviderMemoryIDAsMemoryID(t *testing.T) {
	t.Parallel()

	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		writeHindsightJSON(t, w, map[string]any{"id": "unexpected"})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{})
	event := hindsightTestEvent(`{"provider_memory_id":"doc-only","reason":"superseded"}`)
	_, err := provider.Invalidate(context.Background(), event)
	if err == nil || !strings.Contains(err.Error(), "invalidate requires memory_id") {
		t.Fatalf("Invalidate error = %v, want memory_id requirement", err)
	}
	if attempts != 0 {
		t.Fatalf("upstream attempts = %d, want 0", attempts)
	}
}

func TestHindsightDeleteTreatsMissingDocumentAsIdempotentSuccess(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{})
	event := hindsightTestEvent(`{"document_id":"already-gone"}`)
	result, err := provider.Delete(context.Background(), event)
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
	if result.ProviderMemoryID != "already-gone" || !strings.Contains(string(result.Response), `"already_deleted":true`) {
		t.Errorf("result = %#v", result)
	}
}

func TestHindsightRetryKeepsRetainIdentityStable(t *testing.T) {
	t.Parallel()

	var (
		mu           sync.Mutex
		attempts     int
		operationIDs []string
		documentIDs  []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Items []struct {
				DocumentID string `json:"document_id"`
			} `json:"items"`
			OperationID string `json:"operation_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode retry request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		attempts++
		attempt := attempts
		operationIDs = append(operationIDs, request.OperationID)
		documentIDs = append(documentIDs, request.Items[0].DocumentID)
		mu.Unlock()
		if attempt < 3 {
			http.Error(w, "try later", http.StatusServiceUnavailable)
			return
		}
		writeHindsightJSON(t, w, map[string]any{
			"success":      true,
			"bank_id":      "ignored",
			"items_count":  1,
			"async":        true,
			"operation_id": request.OperationID,
		})
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{
		MaxAttempts:  3,
		RetryBackoff: time.Millisecond,
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})
	if _, err := provider.Retain(context.Background(), hindsightTestEvent(`{"text":"retry safely"}`)); err != nil {
		t.Fatalf("Retain: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	for i := 1; i < len(operationIDs); i++ {
		if operationIDs[i] != operationIDs[0] || documentIDs[i] != documentIDs[0] {
			t.Fatalf("retry identity changed: operation_ids=%#v document_ids=%#v", operationIDs, documentIDs)
		}
	}
}

func TestHindsightDoesNotRetryPermanentHTTPError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{
		MaxAttempts: 5,
		Sleep: func(context.Context, time.Duration) error {
			t.Fatal("Sleep called for a permanent error")
			return nil
		},
	})
	_, err := provider.Retain(context.Background(), hindsightTestEvent(`{"text":"invalid upstream"}`))
	var httpErr *HindsightHTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusBadRequest || httpErr.Retryable {
		t.Fatalf("error = %#v, want non-retryable 400", err)
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestHindsightRequestTimeoutBoundsAllAttempts(t *testing.T) {
	t.Parallel()

	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{
		RequestTimeout: 30 * time.Millisecond,
		MaxAttempts:    3,
	})
	start := time.Now()
	_, err := provider.Recall(context.Background(), MemoryRecallRequest{
		Scope: hindsightTestMemoryScope(),
		Query: "timeout",
	})
	if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Fatalf("Recall error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("request timeout took %v, want bounded total deadline", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("fake server did not receive request")
	}
}

func TestHindsightResponseSizeLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"`+strings.Repeat("x", 256)+`"}`)
	}))
	t.Cleanup(server.Close)

	provider := newTestHindsightProvider(t, server.URL, HindsightConfig{MaxResponseBytes: 64})
	_, err := provider.Health(context.Background())
	if !errors.Is(err, ErrHindsightResponseTooLarge) {
		t.Fatalf("Health error = %v, want ErrHindsightResponseTooLarge", err)
	}
}

func TestHindsightRetryClassification(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
	} {
		if !hindsightRetryableStatus(status) {
			t.Errorf("status %d should be retryable", status)
		}
	}
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusUnprocessableEntity,
	} {
		if hindsightRetryableStatus(status) {
			t.Errorf("status %d should not be retryable", status)
		}
	}
}

func newTestHindsightProvider(t *testing.T, baseURL string, overrides HindsightConfig) *HindsightProvider {
	t.Helper()
	overrides.BaseURL = baseURL
	if overrides.RequestTimeout == 0 {
		overrides.RequestTimeout = time.Second
	}
	if overrides.MaxAttempts == 0 {
		overrides.MaxAttempts = 1
	}
	provider, err := NewHindsightProvider(overrides)
	if err != nil {
		t.Fatalf("NewHindsightProvider: %v", err)
	}
	return provider
}

func hindsightTestEvent(content string) MemoryEventEnvelope {
	return MemoryEventEnvelope{
		SchemaVersion: 1,
		EventType:     "retain",
		Scope: memoryScopeJSON{
			WorkspaceID: hindsightTestWorkspaceA,
			ProjectID:   hindsightTestProject,
			AgentID:     hindsightTestAgent,
			IssueID:     hindsightTestIssue,
			TaskID:      hindsightTestTask,
		},
		Actor:   memoryActorJSON{Type: "agent", ID: hindsightTestAgent},
		Content: json.RawMessage(content),
	}
}

func hindsightTestMemoryScope() MemoryScope {
	return MemoryScope{
		WorkspaceID: util.MustParseUUID(hindsightTestWorkspaceA),
		ProjectID:   util.MustParseUUID(hindsightTestProject),
		AgentID:     util.MustParseUUID(hindsightTestAgent),
		IssueID:     util.MustParseUUID(hindsightTestIssue),
		TaskID:      util.MustParseUUID(hindsightTestTask),
	}
}

func hindsightTestScopeTags() []string {
	return []string{
		"workspace:" + hindsightTestWorkspaceA,
		"project:" + hindsightTestProject,
		"agent:" + hindsightTestAgent,
		"issue:" + hindsightTestIssue,
	}
}

func writeHindsightJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
