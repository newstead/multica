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
	mem0TestWorkspaceID = "11111111-1111-1111-1111-111111111111"
	mem0TestProjectID   = "22222222-2222-2222-2222-222222222222"
	mem0TestAgentID     = "33333333-3333-3333-3333-333333333333"
	mem0TestIssueID     = "44444444-4444-4444-4444-444444444444"
	mem0TestTaskID      = "55555555-5555-5555-5555-555555555555"
)

func TestMapMem0ScopeExactContract(t *testing.T) {
	mapped, err := MapMem0Scope(mem0TestScope())
	if err != nil {
		t.Fatalf("MapMem0Scope: %v", err)
	}
	if mapped.UserID != "multica:workspace:"+mem0TestWorkspaceID {
		t.Fatalf("user_id = %q", mapped.UserID)
	}
	if mapped.AgentID != "multica:agent:"+mem0TestAgentID {
		t.Fatalf("agent_id = %q", mapped.AgentID)
	}
	if mapped.RunID != "multica:task:"+mem0TestTaskID {
		t.Fatalf("run_id = %q", mapped.RunID)
	}
	for key, want := range map[string]string{
		"multica_workspace_id": mem0TestWorkspaceID,
		"multica_project_id":   mem0TestProjectID,
		"multica_agent_id":     mem0TestAgentID,
		"multica_issue_id":     mem0TestIssueID,
		"multica_task_id":      mem0TestTaskID,
	} {
		if got := stringValue(mapped.Filters[key]); got != want {
			t.Errorf("filter %s = %q, want %q", key, got, want)
		}
		if got := stringValue(mapped.Metadata[key]); got != want {
			t.Errorf("metadata %s = %q, want %q", key, got, want)
		}
	}
}

func TestMem0ProviderAuthenticatedAddRecallAndIsolation(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var stored map[string]any
	var addRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "m0sk-test" {
			t.Errorf("X-API-Key = %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "mem0-request-1")

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/search":
			var request struct {
				Query   string         `json:"query"`
				Filters map[string]any `json:"filters"`
			}
			decodeMem0TestJSON(t, r, &request)
			if request.Filters["user_id"] != "multica:workspace:"+mem0TestWorkspaceID {
				t.Errorf("search user_id filter = %#v", request.Filters["user_id"])
			}
			if request.Filters["multica_issue_id"] != mem0TestIssueID {
				t.Errorf("search issue filter = %#v", request.Filters["multica_issue_id"])
			}
			mu.Lock()
			current := stored
			mu.Unlock()
			if _, preflight := request.Filters["multica_idempotency_key"]; preflight {
				if current == nil {
					writeMem0TestJSON(t, w, map[string]any{"results": []any{}})
				} else {
					writeMem0TestJSON(t, w, map[string]any{"results": []any{current}})
				}
				return
			}
			if current == nil {
				t.Error("recall ran before add")
				writeMem0TestJSON(t, w, map[string]any{"results": []any{}})
				return
			}
			own := cloneMap(current)
			own["score"] = 0.91
			foreign := cloneMap(own)
			foreign["id"] = "foreign"
			foreign["user_id"] = "multica:workspace:aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			foreignMetadata := cloneMap(foreign["metadata"].(map[string]any))
			foreignMetadata["multica_workspace_id"] = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
			foreign["metadata"] = foreignMetadata
			writeMem0TestJSON(t, w, map[string]any{"results": []any{foreign, own}})

		case r.Method == http.MethodPost && r.URL.Path == "/memories":
			addRequests++
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("retain request is missing Idempotency-Key")
			}
			var request struct {
				Messages []map[string]string `json:"messages"`
				UserID   string              `json:"user_id"`
				AgentID  string              `json:"agent_id"`
				RunID    string              `json:"run_id"`
				Metadata map[string]any      `json:"metadata"`
			}
			decodeMem0TestJSON(t, r, &request)
			if request.UserID != "multica:workspace:"+mem0TestWorkspaceID ||
				request.AgentID != "multica:agent:"+mem0TestAgentID ||
				request.RunID != "multica:task:"+mem0TestTaskID {
				t.Errorf("native scope mapping = user:%q agent:%q run:%q", request.UserID, request.AgentID, request.RunID)
			}
			if len(request.Messages) != 1 || request.Messages[0]["content"] != "Prefers tea" {
				t.Errorf("messages = %#v", request.Messages)
			}
			storedMemory := map[string]any{
				"id":         "memory-1",
				"memory":     "Prefers tea",
				"user_id":    request.UserID,
				"agent_id":   request.AgentID,
				"run_id":     request.RunID,
				"metadata":   request.Metadata,
				"created_at": "2026-07-29T12:00:00Z",
			}
			mu.Lock()
			stored = storedMemory
			mu.Unlock()
			writeMem0TestJSON(t, w, map[string]any{
				"results": []any{map[string]any{"id": "memory-1", "memory": "Prefers tea", "event": "ADD"}},
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newMem0TestProvider(t, server.URL, Mem0ProviderConfig{})
	ctx := context.Background()
	retained, err := provider.Retain(ctx, mem0TestEnvelope("retain", `{"text":"Prefers tea"}`))
	if err != nil {
		t.Fatalf("Retain: %v", err)
	}
	if retained.ProviderMemoryID != "memory-1" || addRequests != 1 {
		t.Fatalf("retain = %#v, add requests = %d", retained, addRequests)
	}

	recalled, err := provider.Recall(ctx, MemoryRecallRequest{
		Scope: mem0TestScope(),
		Query: "tea preference",
		Limit: 5,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	var results []map[string]any
	if err := json.Unmarshal(recalled.Results, &results); err != nil {
		t.Fatalf("decode recall results: %v", err)
	}
	if len(results) != 1 || results[0]["provider_record_id"] != "memory-1" || results[0]["score"] != 0.91 {
		t.Fatalf("foreign result was not rejected: %#v", results)
	}
	var provenance map[string]any
	if err := json.Unmarshal(recalled.Provenance, &provenance); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	if provenance["provider"] != Mem0ProviderName ||
		provenance["request_id"] != "mem0-request-1" ||
		provenance["provider_results_rejected"] != float64(1) {
		t.Fatalf("provenance = %#v", provenance)
	}
}

func TestMem0ProviderCorrectionHistoryAndDeleteLifecycle(t *testing.T) {
	t.Parallel()

	mapped, err := MapMem0Scope(mem0TestScope())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	memory := mem0TestMemory(mapped, "memory-lifecycle", "Likes coffee", 0)
	history := []any{map[string]any{"event": "ADD", "memory": "Likes coffee"}}
	deleted := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/memories/memory-lifecycle":
			if deleted {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			writeMem0TestJSON(t, w, memory)
		case r.Method == http.MethodPut && r.URL.Path == "/memories/memory-lifecycle":
			var request map[string]any
			decodeMem0TestJSON(t, r, &request)
			if text := stringValue(request["text"]); text != "" {
				memory["memory"] = text
				history = append(history, map[string]any{"event": "UPDATE", "memory": text})
			}
			if metadata, ok := request["metadata"].(map[string]any); ok {
				if metadata["multica_workspace_id"] != mem0TestWorkspaceID {
					t.Errorf("protected workspace metadata changed: %#v", metadata)
				}
				memory["metadata"] = metadata
			}
			writeMem0TestJSON(t, w, map[string]any{"message": "Memory updated successfully"})
		case r.Method == http.MethodGet && r.URL.Path == "/memories/memory-lifecycle/history":
			writeMem0TestJSON(t, w, history)
		case r.Method == http.MethodDelete && r.URL.Path == "/memories/memory-lifecycle":
			deleted = true
			writeMem0TestJSON(t, w, map[string]any{"message": "Memory deleted successfully"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := newMem0TestProvider(t, server.URL, Mem0ProviderConfig{})

	got, err := provider.Get(context.Background(), mem0TestScope(), "memory-lifecycle")
	if err != nil || !strings.Contains(string(got), "Likes coffee") {
		t.Fatalf("Get = %s, %v", got, err)
	}

	update := mem0TestEnvelope("update", `{
		"memory_id":"memory-lifecycle",
		"text":"Prefers tea",
		"metadata":{"category":"preference","multica_workspace_id":"foreign"}
	}`)
	if _, err := provider.Update(context.Background(), update); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err = provider.Get(context.Background(), mem0TestScope(), "memory-lifecycle")
	if err != nil || !strings.Contains(string(got), "Prefers tea") || !strings.Contains(string(got), `"category":"preference"`) {
		t.Fatalf("updated Get = %s, %v", got, err)
	}

	gotHistory, err := provider.History(context.Background(), mem0TestScope(), "memory-lifecycle")
	if err != nil || !strings.Contains(string(gotHistory), "UPDATE") {
		t.Fatalf("History = %s, %v", gotHistory, err)
	}
	deletion := mem0TestEnvelope("delete", `{"memory_id":"memory-lifecycle"}`)
	if _, err := provider.Delete(context.Background(), deletion); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if !deleted {
		t.Fatal("delete endpoint was not called")
	}
}

func TestMem0ProviderTimeoutRetryUsesIdempotencyPreflight(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var stored map[string]any
	var addCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/search":
			var request struct {
				Filters map[string]any `json:"filters"`
			}
			decodeMem0TestJSON(t, r, &request)
			mu.Lock()
			current := stored
			mu.Unlock()
			if current != nil && current["metadata"].(map[string]any)["multica_idempotency_key"] == request.Filters["multica_idempotency_key"] {
				writeMem0TestJSON(t, w, map[string]any{"results": []any{current}})
				return
			}
			writeMem0TestJSON(t, w, map[string]any{"results": []any{}})
		case r.Method == http.MethodPost && r.URL.Path == "/memories":
			var request struct {
				UserID   string         `json:"user_id"`
				AgentID  string         `json:"agent_id"`
				RunID    string         `json:"run_id"`
				Metadata map[string]any `json:"metadata"`
			}
			decodeMem0TestJSON(t, r, &request)
			mu.Lock()
			addCount++
			stored = map[string]any{
				"id":       "memory-timeout",
				"memory":   "Durably stored before timeout",
				"user_id":  request.UserID,
				"agent_id": request.AgentID,
				"run_id":   request.RunID,
				"metadata": request.Metadata,
			}
			mu.Unlock()
			time.Sleep(250 * time.Millisecond)
			writeMem0TestJSON(t, w, map[string]any{"results": []any{map[string]any{"id": "memory-timeout"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider := newMem0TestProvider(t, server.URL, Mem0ProviderConfig{
		RequestTimeout: 100 * time.Millisecond,
		MaxAttempts:    2,
		Sleep:          sleepContext,
	})
	result, err := provider.Retain(context.Background(), mem0TestEnvelope("retain", `{"text":"Durably stored before timeout"}`))
	if err != nil {
		t.Fatalf("Retain after timeout: %v", err)
	}
	if result.ProviderMemoryID != "memory-timeout" {
		t.Fatalf("provider memory ID = %q", result.ProviderMemoryID)
	}
	mu.Lock()
	defer mu.Unlock()
	if addCount != 1 {
		t.Fatalf("POST /memories count = %d, want exactly 1", addCount)
	}
}

func TestMem0ProviderRetryResponseAndTokenBounds(t *testing.T) {
	t.Parallel()

	var healthAttempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/configure" {
			attempt := healthAttempts.Add(1)
			if attempt < 3 {
				http.Error(w, `{"detail":"temporarily unavailable"}`, http.StatusServiceUnavailable)
				return
			}
			writeMem0TestJSON(t, w, map[string]any{"version": "v1.1"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	provider := newMem0TestProvider(t, server.URL, Mem0ProviderConfig{MaxAttempts: 3})
	health, err := provider.Health(context.Background())
	if err != nil || !health.OK || healthAttempts.Load() != 3 {
		t.Fatalf("Health = %#v, attempts %d, err %v", health, healthAttempts.Load(), err)
	}

	oversizedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"payload":"%s"}`, strings.Repeat("x", 256))
	}))
	defer oversizedServer.Close()
	bounded := newMem0TestProvider(t, oversizedServer.URL, Mem0ProviderConfig{
		MaxAttempts:      1,
		MaxResponseBytes: 64,
	})
	_, err = bounded.Health(context.Background())
	if !errors.Is(err, ErrMem0ResponseTooLarge) {
		t.Fatalf("oversized response error = %v", err)
	}

	mapped, err := MapMem0Scope(mem0TestScope())
	if err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("memory ", 80)
	results := []any{
		mem0TestMemory(mapped, "one", longText, 0.9),
		mem0TestMemory(mapped, "two", longText, 0.8),
		mem0TestMemory(mapped, "three", longText, 0.7),
	}
	recallServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMem0TestJSON(t, w, map[string]any{"results": results})
	}))
	defer recallServer.Close()
	budgeted := newMem0TestProvider(t, recallServer.URL, Mem0ProviderConfig{
		MaxRecallTokens: 60,
		MaxMemoryTokens: 20,
	})
	recalled, err := budgeted.Recall(context.Background(), MemoryRecallRequest{
		Scope: mem0TestScope(),
		Query: "memory",
		Limit: 3,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}
	var items []map[string]any
	if err := json.Unmarshal(recalled.Results, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || approximateTokens(stringValue(items[0]["text"])) > 20 {
		t.Fatalf("budgeted items = %#v", items)
	}
	var provenance map[string]any
	if err := json.Unmarshal(recalled.Provenance, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance["results_truncated"].(float64) < 1 {
		t.Fatalf("truncation provenance = %#v", provenance)
	}
}

func TestMem0ProviderRecallBudgetsSerializedPayloadWithLargeMetadata(t *testing.T) {
	t.Parallel()

	mapped, err := MapMem0Scope(mem0TestScope())
	if err != nil {
		t.Fatal(err)
	}
	memory := mem0TestMemory(mapped, "bounded-memory", "Prefers concise answers", 0.95)
	metadata := memory["metadata"].(map[string]any)
	metadata["source_type"] = "issue_comment"
	metadata["source_id"] = "66666666-6666-6666-6666-666666666666"
	metadata["source_version"] = "7"
	metadata["provider_controlled_blob"] = strings.Repeat("untrusted", 16*1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeMem0TestJSON(t, w, map[string]any{"results": []any{memory}})
	}))
	defer server.Close()

	const tokenBudget = 80
	provider := newMem0TestProvider(t, server.URL, Mem0ProviderConfig{
		MaxRecallTokens: tokenBudget,
		MaxMemoryTokens: 20,
	})
	recalled, err := provider.Recall(context.Background(), MemoryRecallRequest{
		Scope: mem0TestScope(),
		Query: "response preference",
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("Recall: %v", err)
	}

	injectedTokens := approximateTokens(string(recalled.Results))
	if injectedTokens > tokenBudget {
		t.Fatalf("serialized recall payload uses %d tokens, budget %d: %s", injectedTokens, tokenBudget, recalled.Results)
	}
	var items []map[string]any
	if err := json.Unmarshal(recalled.Results, &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("recall items = %#v", items)
	}
	if len(items[0]) != 4 || items[0]["text"] != "Prefers concise answers" {
		t.Fatalf("injected result is not the strict projection: %#v", items[0])
	}
	if _, ok := items[0]["metadata"]; ok {
		t.Fatalf("provider metadata leaked into injected result: %#v", items[0])
	}

	var provenance map[string]any
	if err := json.Unmarshal(recalled.Provenance, &provenance); err != nil {
		t.Fatal(err)
	}
	if estimated := int(provenance["estimated_injected_tokens"].(float64)); estimated != injectedTokens || estimated > tokenBudget {
		t.Fatalf("estimated injected tokens = %d, actual = %d, budget = %d", estimated, injectedTokens, tokenBudget)
	}
	provenanceItems := provenance["items"].([]any)
	source := provenanceItems[0].(map[string]any)["source"].(map[string]any)
	if source["source_type"] != "issue_comment" || source["source_id"] != "66666666-6666-6666-6666-666666666666" || source["source_version"] != "7" {
		t.Fatalf("source provenance = %#v", source)
	}
	if strings.Contains(string(recalled.Provenance), "provider_controlled_blob") {
		t.Fatalf("arbitrary provider metadata leaked into provenance")
	}
}

func newMem0TestProvider(t *testing.T, baseURL string, overrides Mem0ProviderConfig) *Mem0Provider {
	t.Helper()
	overrides.BaseURL = baseURL
	overrides.APIKey = "m0sk-test"
	if overrides.Sleep == nil {
		overrides.Sleep = func(context.Context, time.Duration) error { return nil }
	}
	provider, err := NewMem0Provider(overrides)
	if err != nil {
		t.Fatalf("NewMem0Provider: %v", err)
	}
	return provider
}

func mem0TestScope() MemoryScope {
	return MemoryScope{
		WorkspaceID: util.MustParseUUID(mem0TestWorkspaceID),
		ProjectID:   util.MustParseUUID(mem0TestProjectID),
		AgentID:     util.MustParseUUID(mem0TestAgentID),
		IssueID:     util.MustParseUUID(mem0TestIssueID),
		TaskID:      util.MustParseUUID(mem0TestTaskID),
	}
}

func mem0TestEnvelope(eventType, content string) MemoryEventEnvelope {
	scope := mem0TestScope()
	return MemoryEventEnvelope{
		SchemaVersion: 1,
		EventType:     eventType,
		Scope: memoryScopeJSON{
			WorkspaceID: uuidString(scope.WorkspaceID),
			ProjectID:   uuidString(scope.ProjectID),
			AgentID:     uuidString(scope.AgentID),
			IssueID:     uuidString(scope.IssueID),
			TaskID:      uuidString(scope.TaskID),
		},
		Actor:   memoryActorJSON{Type: "agent", ID: mem0TestAgentID},
		Content: json.RawMessage(content),
		Metadata: map[string]any{
			"source_type":    "issue_comment",
			"source_id":      "66666666-6666-6666-6666-666666666666",
			"source_version": "1",
		},
	}
}

func mem0TestMemory(mapped Mem0ScopeMapping, id, text string, score float64) map[string]any {
	return map[string]any{
		"id":       id,
		"memory":   text,
		"user_id":  mapped.UserID,
		"agent_id": mapped.AgentID,
		"run_id":   mapped.RunID,
		"score":    score,
		"metadata": cloneMap(mapped.Metadata),
	}
}

func decodeMem0TestJSON(t *testing.T, r *http.Request, target any) {
	t.Helper()
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		t.Errorf("decode request: %v", err)
	}
}

func writeMem0TestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
