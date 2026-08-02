package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// Resolution unit tests (no DB) — the precedence rule agent > project >
// workspace and the "invalid/empty inherits upward" fallback.
// ---------------------------------------------------------------------------

func TestResolveLanguagePolicy(t *testing.T) {
	ru := pgtype.Text{String: "ru", Valid: true}
	en := pgtype.Text{String: "en", Valid: true}
	cases := []struct {
		name      string
		agent     pgtype.Text
		project   pgtype.Text
		workspace pgtype.Text
		want      string
	}{
		{"agent wins", ru, en, en, "ru"},
		{"project over workspace", pgtype.Text{}, ru, en, "ru"},
		{"workspace fallback", pgtype.Text{}, pgtype.Text{}, ru, "ru"},
		{"nothing set", pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, ""},
		{"invalid agent inherits project", pgtype.Text{String: "xx", Valid: true}, en, pgtype.Text{}, "en"},
		{"empty agent inherits project", pgtype.Text{String: "", Valid: true}, en, pgtype.Text{}, "en"},
		{"invalid project inherits workspace", pgtype.Text{}, pgtype.Text{String: "fr", Valid: true}, ru, "ru"},
	}
	for _, tc := range cases {
		if got := resolveLanguagePolicy(tc.agent, tc.project, tc.workspace); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestValidateLanguagePolicy(t *testing.T) {
	for _, v := range []string{"ru", "en", "zh-Hans", "ja", "ko"} {
		if !validateLanguagePolicy(v) {
			t.Errorf("validateLanguagePolicy(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "RU", "fr", "zh-hans", "ZH-HANS", "ru-ru"} {
		if validateLanguagePolicy(v) {
			t.Errorf("validateLanguagePolicy(%q) = true, want false", v)
		}
	}
}

// TestSupportedLanguagePoliciesMatchCore guards the single-source-of-truth
// contract: AGENT_LANGUAGE_POLICY_VALUES in
// packages/core/types/language-policy.ts is canonical, and the Go allow-list
// must mirror it exactly. go test runs with the working directory set to this
// package, so the relative path resolves to the monorepo checkout.
func TestSupportedLanguagePoliciesMatchCore(t *testing.T) {
	path := filepath.Join("..", "..", "..", "packages", "core", "types", "language-policy.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read core language policy types: %v", err)
	}
	m := regexp.MustCompile(`AGENT_LANGUAGE_POLICY_VALUES\s*=\s*\[([^\]]*)\]`).FindSubmatch(raw)
	if m == nil {
		t.Fatalf("AGENT_LANGUAGE_POLICY_VALUES array not found in %s", path)
	}
	var core []string
	for _, lit := range regexp.MustCompile(`"([^"]+)"`).FindAllSubmatch(m[1], -1) {
		core = append(core, string(lit[1]))
	}
	if !slices.Equal(core, languagePolicyValues) {
		t.Fatalf(
			"core list %v != Go list %v: keep server/internal/handler/language_policy.go (and migration 239) in sync with packages/core/types/language-policy.ts",
			core, languagePolicyValues,
		)
	}
}

// ---------------------------------------------------------------------------
// DB-backed claim wiring test — resolves agent > project > workspace at claim
// and returns the effective policy on the task response.
// ---------------------------------------------------------------------------

func TestClaimTaskByRuntime_LanguagePolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	cases := []struct {
		name            string
		agentPolicy     string
		projectPolicy   string
		workspacePolicy string
		want            string
	}{
		{"nothing set", "", "", "", ""},
		{"workspace only", "", "", "ru", "ru"},
		{"project overrides workspace", "", "en", "ru", "en"},
		{"agent overrides project", "ru", "en", "ru", "ru"},
		{"non-ru/en code flows through the chain", "ja", "en", "ru", "ja"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := claimLanguagePolicyForTest(t, tc.agentPolicy, tc.projectPolicy, tc.workspacePolicy)
			if got != tc.want {
				t.Fatalf("claim language_policy = %q, want %q", got, tc.want)
			}
		})
	}
}

func claimLanguagePolicyForTest(t *testing.T, agentPolicy, projectPolicy, workspacePolicy string) string {
	t.Helper()
	ctx := context.Background()

	// Workspace policy is shared fixture state: snapshot and restore.
	var prev *string
	if err := testPool.QueryRow(ctx, `SELECT language_policy FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prev); err != nil {
		t.Fatalf("load workspace language_policy: %v", err)
	}
	t.Cleanup(func() {
		if prev == nil {
			testPool.Exec(context.Background(), `UPDATE workspace SET language_policy = NULL WHERE id = $1`, testWorkspaceID)
		} else {
			testPool.Exec(context.Background(), `UPDATE workspace SET language_policy = $2 WHERE id = $1`, testWorkspaceID, *prev)
		}
	})
	if workspacePolicy == "" {
		if _, err := testPool.Exec(ctx, `UPDATE workspace SET language_policy = NULL WHERE id = $1`, testWorkspaceID); err != nil {
			t.Fatalf("clear workspace language_policy: %v", err)
		}
	} else {
		if _, err := testPool.Exec(ctx, `UPDATE workspace SET language_policy = $2 WHERE id = $1`, testWorkspaceID, workspacePolicy); err != nil {
			t.Fatalf("set workspace language_policy: %v", err)
		}
	}

	agentID := createHandlerTestAgent(t, "LangPolicyClaimAgent-"+uuid.NewString()[:8], []byte("[]"))
	if agentPolicy != "" {
		if _, err := testPool.Exec(ctx, `UPDATE agent SET language_policy = $2 WHERE id = $1`, agentID, agentPolicy); err != nil {
			t.Fatalf("set agent language_policy: %v", err)
		}
	}

	projectID := ""
	if projectPolicy != "" {
		var pid string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO project (workspace_id, title, language_policy)
			VALUES ($1, 'Language policy claim project', $2)
			RETURNING id
		`, testWorkspaceID, projectPolicy).Scan(&pid); err != nil {
			t.Fatalf("create project: %v", err)
		}
		projectID = pid
		t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, pid) })
	}

	issueID := createLanguagePolicyClaimIssue(t, projectID)
	runtimeID := handlerTestRuntimeID(t)

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 1000)
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
		t.Fatalf("create claim task: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(
		http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/claim",
		nil,
		testWorkspaceID,
		"language-policy-claim-test",
	)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ClaimTaskByRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Task *struct {
			LanguagePolicy string `json:"language_policy"`
		} `json:"task"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode claim response: %v", err)
	}
	if response.Task == nil {
		t.Fatal("claim response missing task")
	}
	return response.Task.LanguagePolicy
}

func createLanguagePolicyClaimIssue(t *testing.T, projectID string) string {
	t.Helper()
	ctx := context.Background()

	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, project_id)
		VALUES (
			$1, 'Language policy claim issue', 'todo', 'none', $2, 'member',
			(SELECT COALESCE(MAX(number), 90000) + 1 FROM issue WHERE workspace_id = $1),
			0, NULLIF($3, '')::uuid
		)
		RETURNING id
	`, testWorkspaceID, testUserID, projectID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID) })
	return issueID
}

// ---------------------------------------------------------------------------
// Write-path tests — API set/clear/validate for workspace and agent policies.
// ---------------------------------------------------------------------------

func TestUpdateWorkspace_LanguagePolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Shared fixture: snapshot and restore.
	var prev *string
	if err := testPool.QueryRow(ctx, `SELECT language_policy FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prev); err != nil {
		t.Fatalf("load workspace language_policy: %v", err)
	}
	t.Cleanup(func() {
		if prev == nil {
			testPool.Exec(context.Background(), `UPDATE workspace SET language_policy = NULL WHERE id = $1`, testWorkspaceID)
		} else {
			testPool.Exec(context.Background(), `UPDATE workspace SET language_policy = $2 WHERE id = $1`, testWorkspaceID, *prev)
		}
	})

	// Set.
	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/workspaces/"+testWorkspaceID, map[string]any{"language_policy": "ru"})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateWorkspace set: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got *string
	if err := testPool.QueryRow(ctx, `SELECT language_policy FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&got); err != nil {
		t.Fatalf("load updated workspace policy: %v", err)
	}
	if got == nil || *got != "ru" {
		t.Fatalf("workspace language_policy = %v, want ru", got)
	}

	// Unsupported value is rejected.
	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/workspaces/"+testWorkspaceID, map[string]any{"language_policy": "fr"})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateWorkspace invalid: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Explicit null clears back to unset.
	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/workspaces/"+testWorkspaceID, map[string]any{"language_policy": nil})
	req = withURLParam(req, "id", testWorkspaceID)
	testHandler.UpdateWorkspace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateWorkspace clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cleared bool
	if err := testPool.QueryRow(ctx, `SELECT language_policy IS NULL FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&cleared); err != nil {
		t.Fatalf("verify cleared workspace policy: %v", err)
	}
	if !cleared {
		t.Fatal("workspace language_policy not cleared after explicit null")
	}
}

func TestUpdateAgent_LanguagePolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	agentID := createHandlerTestAgent(t, "LangPolicyUpdateAgent-"+uuid.NewString()[:8], []byte("[]"))

	// Set.
	w := httptest.NewRecorder()
	req := newRequest("PATCH", "/api/agents/"+agentID, map[string]any{"language_policy": "ru"})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent set: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got *string
	if err := testPool.QueryRow(ctx, `SELECT language_policy FROM agent WHERE id = $1`, agentID).Scan(&got); err != nil {
		t.Fatalf("load updated agent policy: %v", err)
	}
	if got == nil || *got != "ru" {
		t.Fatalf("agent language_policy = %v, want ru", got)
	}

	// Unsupported value is rejected.
	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/agents/"+agentID, map[string]any{"language_policy": "de"})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateAgent invalid: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Empty string clears back to unset.
	w = httptest.NewRecorder()
	req = newRequest("PATCH", "/api/agents/"+agentID, map[string]any{"language_policy": ""})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateAgent clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cleared bool
	if err := testPool.QueryRow(ctx, `SELECT language_policy IS NULL FROM agent WHERE id = $1`, agentID).Scan(&cleared); err != nil {
		t.Fatalf("verify cleared agent policy: %v", err)
	}
	if !cleared {
		t.Fatal("agent language_policy not cleared after empty string")
	}
}

func TestUpdateProject_LanguagePolicy(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Seed a project through the API and clean it up afterwards.
	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/projects?workspace_id="+testWorkspaceID, map[string]any{
		"title": "Language policy update project",
	})
	testHandler.CreateProject(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed CreateProject: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var project ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&project); err != nil {
		t.Fatalf("decode CreateProject: %v", err)
	}
	t.Cleanup(func() {
		req := newRequest("DELETE", "/api/projects/"+project.ID, nil)
		req = withURLParam(req, "id", project.ID)
		testHandler.DeleteProject(httptest.NewRecorder(), req)
	})

	// Set a supported code (beyond the original ru/en pair).
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+project.ID, map[string]any{"language_policy": "zh-Hans"})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProject set: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var got *string
	if err := testPool.QueryRow(ctx, `SELECT language_policy FROM project WHERE id = $1`, project.ID).Scan(&got); err != nil {
		t.Fatalf("load updated project policy: %v", err)
	}
	if got == nil || *got != "zh-Hans" {
		t.Fatalf("project language_policy = %v, want zh-Hans", got)
	}

	// Unsupported value is rejected.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+project.ID, map[string]any{"language_policy": "de"})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("UpdateProject invalid: expected 400, got %d: %s", w.Code, w.Body.String())
	}

	// Explicit null clears back to unset.
	w = httptest.NewRecorder()
	req = newRequest("PUT", "/api/projects/"+project.ID, map[string]any{"language_policy": nil})
	req = withURLParam(req, "id", project.ID)
	testHandler.UpdateProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateProject clear: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var cleared bool
	if err := testPool.QueryRow(ctx, `SELECT language_policy IS NULL FROM project WHERE id = $1`, project.ID).Scan(&cleared); err != nil {
		t.Fatalf("verify cleared project policy: %v", err)
	}
	if !cleared {
		t.Fatal("project language_policy not cleared after explicit null")
	}
}
