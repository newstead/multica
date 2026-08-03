package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
)

func rolePolicyURL() string { return "/api/workspaces/" + testWorkspaceID + "/role-policy" }

func rolePolicyGetRequest() *http.Request {
	req := newRequestAs(testUserID, http.MethodGet, rolePolicyURL(), nil)
	return withURLParam(req, "id", testWorkspaceID)
}

func rolePolicyPutRequest(body any) *http.Request {
	req := newRequestAs(testUserID, http.MethodPut, rolePolicyURL(), body)
	return withURLParam(req, "id", testWorkspaceID)
}

func seedRolePolicyRuleForTest(t *testing.T, roleCode, agentID, runtimeID, model, thinkingLevel, serviceTier, fallback string) {
	t.Helper()
	if fallback == "" {
		fallback = "agent_default"
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO workspace_role_policy (
			workspace_id, role_code, agent_id, runtime_id, model, thinking_level, service_tier, fallback
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (workspace_id, role_code) DO UPDATE SET
			agent_id = EXCLUDED.agent_id,
			runtime_id = EXCLUDED.runtime_id,
			model = EXCLUDED.model,
			thinking_level = EXCLUDED.thinking_level,
			service_tier = EXCLUDED.service_tier,
			fallback = EXCLUDED.fallback,
			updated_at = now()
	`, testWorkspaceID, roleCode, nullableUUID(agentID), nullableUUID(runtimeID), nullableString(model), nullableString(thinkingLevel), nullableString(serviceTier), fallback); err != nil {
		t.Fatalf("seed role policy rule: %v", err)
	}
}

func clearRolePolicyForTest(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`DELETE FROM workspace_role_policy WHERE workspace_id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("clear role policy rules: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET role_policy_enabled = false WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("disable role policy: %v", err)
	}
}

func nullableUUID(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func TestGetWorkspaceRolePolicy_Roundtrip(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	agentID := createHandlerTestAgent(t, "role-policy-agent", nil)
	runtimeID := handlerTestRuntimeID(t)

	seedRolePolicyRuleForTest(t, "BE", "", runtimeID, "gpt-5", "high", "fast", "agent_default")
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET role_policy_enabled = true WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("enable role policy: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.GetWorkspaceRolePolicy(w, rolePolicyGetRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("GET role-policy: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp rolePolicyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if !resp.Enabled {
		t.Fatalf("enabled = false, want true")
	}
	rule, ok := resp.Rules["BE"]
	if !ok {
		t.Fatalf("rules missing BE: %+v", resp.Rules)
	}
	if rule.RuntimeID == nil || *rule.RuntimeID != runtimeID {
		t.Fatalf("BE runtime_id = %v, want %s", rule.RuntimeID, runtimeID)
	}
	if rule.Model == nil || *rule.Model != "gpt-5" {
		t.Fatalf("BE model = %v, want gpt-5", rule.Model)
	}
	if rule.AgentID != nil || rule.ThinkingLevel == nil || *rule.ThinkingLevel != "high" {
		t.Fatalf("BE rule decoded incorrectly: %+v", rule)
	}
	_ = agentID
}

func TestUpdateWorkspaceRolePolicy_RoundtripAndValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	agentID := createHandlerTestAgent(t, "role-policy-agent-2", nil)
	runtimeID := handlerTestRuntimeID(t)

	// Full matrix replacement: bind_agent for QA, exec_override for BE.
	body := map[string]any{
		"enabled": true,
		"rules": map[string]any{
			"QA": map[string]any{"agent_id": agentID, "fallback": "agent_default"},
			"BE": map[string]any{
				"runtime_id":     runtimeID,
				"model":          "gpt-5",
				"thinking_level": "high",
				"service_tier":   "fast",
				"fallback":       "agent_default",
			},
		},
	}
	w := httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(body))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT role-policy: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var resp rolePolicyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode PUT response: %v", err)
	}
	if !resp.Enabled {
		t.Fatalf("enabled = false, want true")
	}
	qa := resp.Rules["QA"]
	if qa.AgentID == nil || *qa.AgentID != agentID {
		t.Fatalf("QA agent_id = %v, want %s", qa.AgentID, agentID)
	}
	be := resp.Rules["BE"]
	if be.RuntimeID == nil || *be.RuntimeID != runtimeID || be.Model == nil || *be.Model != "gpt-5" {
		t.Fatalf("BE rule roundtrip mismatch: %+v", be)
	}
	if _, ok := resp.Rules["TL"]; ok {
		t.Fatalf("TL rule must not exist after full replacement")
	}

	// GET reflects the same matrix.
	w2 := httptest.NewRecorder()
	testHandler.GetWorkspaceRolePolicy(w2, rolePolicyGetRequest())
	if w2.Code != http.StatusOK {
		t.Fatalf("GET after PUT: got %d, want 200", w2.Code)
	}
	var got rolePolicyResponse
	json.Unmarshal(w2.Body.Bytes(), &got)
	if !got.Enabled || len(got.Rules) != 2 {
		t.Fatalf("GET after PUT mismatch: %+v", got)
	}
}

func TestUpdateWorkspaceRolePolicy_Validation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	agentID := createHandlerTestAgent(t, "role-policy-agent-3", nil)

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{
			name: "bad role code",
			body: map[string]any{"enabled": true, "rules": map[string]any{"XX": map[string]any{"fallback": "agent_default"}}},
			want: http.StatusBadRequest,
		},
		{
			name: "role code not normalized",
			body: map[string]any{"enabled": true, "rules": map[string]any{"be ": map[string]any{"fallback": "agent_default"}}},
			want: http.StatusOK,
		},
		{
			name: "foreign agent",
			body: map[string]any{"enabled": true, "rules": map[string]any{"BE": map[string]any{"agent_id": "00000000-0000-0000-0000-00000000dead", "fallback": "agent_default"}}},
			want: http.StatusBadRequest,
		},
		{
			name: "agent and exec fields together",
			body: map[string]any{"enabled": true, "rules": map[string]any{"BE": map[string]any{"agent_id": agentID, "model": "gpt-5", "fallback": "agent_default"}}},
			want: http.StatusBadRequest,
		},
		{
			name: "exec fields without runtime",
			body: map[string]any{"enabled": true, "rules": map[string]any{"BE": map[string]any{"model": "gpt-5", "fallback": "agent_default"}}},
			want: http.StatusBadRequest,
		},
		{
			name: "bad fallback",
			body: map[string]any{"enabled": true, "rules": map[string]any{"BE": map[string]any{"fallback": "bogus"}}},
			want: http.StatusBadRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(tc.body))
			if w.Code != tc.want {
				t.Fatalf("PUT role-policy: got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}

	// Archived agent is rejected.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET archived_at = now() WHERE id = $1`, agentID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `UPDATE agent SET archived_at = NULL WHERE id = $1`, agentID)
	})
	w := httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(map[string]any{
		"enabled": true,
		"rules":   map[string]any{"BE": map[string]any{"agent_id": agentID, "fallback": "agent_default"}},
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("archived agent: got %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestRolePolicyAdminRoutesRequireWorkspaceAdmin(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	memberID := createPermissionTestMember(t, fmt.Sprintf("role-policy-member-%d@multica.test", time.Now().UnixNano()))

	router := chi.NewRouter()
	router.Route("/api/workspaces/{id}", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceMemberFromURL(testHandler.Queries, "id"))
			r.Get("/role-policy", testHandler.GetWorkspaceRolePolicy)
		})
		r.Group(func(r chi.Router) {
			r.Use(middleware.RequireWorkspaceRoleFromURL(testHandler.Queries, "id", "owner", "admin"))
			r.Put("/role-policy", testHandler.UpdateWorkspaceRolePolicy)
		})
	})

	// Member can read.
	req := newRequestAs(memberID, http.MethodGet, rolePolicyURL(), nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("member GET role-policy: got %d, want 200", rec.Code)
	}

	// Member cannot write.
	req = newRequestAs(memberID, http.MethodPut, rolePolicyURL(), map[string]any{"enabled": false, "rules": map[string]any{}})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member PUT role-policy: got %d, want 403", rec.Code)
	}

	// Owner can write.
	req = newRequestAs(testUserID, http.MethodPut, rolePolicyURL(), map[string]any{"enabled": false, "rules": map[string]any{}})
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner PUT role-policy: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestRolePolicyCatalogValidation(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	runtimeID := handlerTestRuntimeID(t)

	// Seed a cached catalog: model gpt-5 with thinking levels + service tiers.
	cache := NewInMemoryModelCatalogCache()
	old := testHandler.ModelCatalogCache
	testHandler.ModelCatalogCache = cache
	t.Cleanup(func() { testHandler.ModelCatalogCache = old })
	if err := cache.Put(context.Background(), runtimeID, []ModelEntry{
		{
			ID:      "gpt-5",
			Label:   "GPT-5",
			Default: true,
			Thinking: &ModelThinking{SupportedLevels: []ThinkingLevel{
				{Value: "low", Label: "Low"},
				{Value: "high", Label: "High"},
			}},
			ServiceTiers: []ModelServiceTier{{ID: "fast", Name: "Fast"}},
		},
	}, true); err != nil {
		t.Fatalf("seed catalog: %v", err)
	}

	ok := map[string]any{
		"enabled": true,
		"rules": map[string]any{
			"BE": map[string]any{"runtime_id": runtimeID, "model": "gpt-5", "thinking_level": "high", "service_tier": "fast", "fallback": "agent_default"},
		},
	}
	w := httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(ok))
	if w.Code != http.StatusOK {
		t.Fatalf("valid catalog values: got %d, want 200: %s", w.Code, w.Body.String())
	}

	badTier := map[string]any{
		"enabled": true,
		"rules": map[string]any{
			"BE": map[string]any{"runtime_id": runtimeID, "model": "gpt-5", "service_tier": "turbo", "fallback": "agent_default"},
		},
	}
	w = httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(badTier))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid service_tier: got %d, want 400: %s", w.Code, w.Body.String())
	}

	badThinking := map[string]any{
		"enabled": true,
		"rules": map[string]any{
			"BE": map[string]any{"runtime_id": runtimeID, "model": "gpt-5", "thinking_level": "ultra", "fallback": "agent_default"},
		},
	}
	w = httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(badThinking))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid thinking_level: got %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestWorkspaceRolePolicy_SerializationCompatibility verifies the JSON wire
// shape is stable (the GET body feeds PUT verbatim and the matrix survives
// unchanged) and that updating the role policy never clobbers unrelated
// workspace state (language_policy, settings).
func TestWorkspaceRolePolicy_SerializationCompatibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	agentID := createHandlerTestAgent(t, "role-policy-serial-agent", nil)
	runtimeID := handlerTestRuntimeID(t)

	// Seed unrelated workspace state that a role-policy update must preserve.
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE workspace
		SET language_policy = 'ru',
		    settings = jsonb_build_object('comment_routing', jsonb_build_object('escalation_seconds', 900))
		WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("seed workspace language_policy/settings: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `UPDATE workspace SET language_policy = NULL, settings = '{}'::jsonb WHERE id = $1`, testWorkspaceID)
	})

	// Rules for two roles: bind_agent (QA) and exec_override (BE).
	seedRolePolicyRuleForTest(t, "QA", agentID, "", "", "", "", "agent_default")
	seedRolePolicyRuleForTest(t, "BE", "", runtimeID, "gpt-5", "high", "fast", "disabled")
	if _, err := testPool.Exec(ctx,
		`UPDATE workspace SET role_policy_enabled = true WHERE id = $1`, testWorkspaceID); err != nil {
		t.Fatalf("enable role policy: %v", err)
	}

	// Capture the GET wire shape...
	w := httptest.NewRecorder()
	testHandler.GetWorkspaceRolePolicy(w, rolePolicyGetRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("GET role-policy: got %d, want 200: %s", w.Code, w.Body.String())
	}
	var wire map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode GET wire shape: %v", err)
	}

	// ...and feed it back to PUT verbatim (same field names and values).
	w = httptest.NewRecorder()
	testHandler.UpdateWorkspaceRolePolicy(w, rolePolicyPutRequest(wire))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT verbatim GET body: got %d, want 200: %s", w.Code, w.Body.String())
	}

	// GET again: toggle and matrix must be identical after the verbatim PUT.
	w = httptest.NewRecorder()
	testHandler.GetWorkspaceRolePolicy(w, rolePolicyGetRequest())
	if w.Code != http.StatusOK {
		t.Fatalf("GET after verbatim PUT: got %d, want 200", w.Code)
	}
	var got rolePolicyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode second GET: %v", err)
	}
	if !got.Enabled || len(got.Rules) != 2 {
		t.Fatalf("matrix changed by verbatim PUT: %+v", got)
	}
	qa := got.Rules["QA"]
	if qa.AgentID == nil || *qa.AgentID != agentID {
		t.Fatalf("QA rule changed by verbatim PUT: %+v", qa)
	}
	be := got.Rules["BE"]
	if be.RuntimeID == nil || *be.RuntimeID != runtimeID || be.Model == nil || *be.Model != "gpt-5" ||
		be.ThinkingLevel == nil || *be.ThinkingLevel != "high" ||
		be.ServiceTier == nil || *be.ServiceTier != "fast" || be.Fallback != "disabled" {
		t.Fatalf("BE rule changed by verbatim PUT: %+v", be)
	}

	// Unrelated workspace state must survive the update untouched.
	var languagePolicy *string
	var settings []byte
	if err := testPool.QueryRow(ctx,
		`SELECT language_policy, settings FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&languagePolicy, &settings); err != nil {
		t.Fatalf("reload workspace state: %v", err)
	}
	if languagePolicy == nil || *languagePolicy != "ru" {
		t.Fatalf("language_policy = %v, want ru", languagePolicy)
	}
	var settingsDoc map[string]any
	if err := json.Unmarshal(settings, &settingsDoc); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	cr, ok := settingsDoc["comment_routing"].(map[string]any)
	if !ok {
		t.Fatalf("settings.comment_routing missing after role-policy PUT: %s", settings)
	}
	if esc, ok := cr["escalation_seconds"].(float64); !ok || esc != 900 {
		t.Fatalf("settings.comment_routing.escalation_seconds = %v, want 900", cr["escalation_seconds"])
	}
}
