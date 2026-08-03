package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// workspace_role_policy deliberately carries no agent/runtime foreign keys
// (CLAUDE.md), so runtime teardown must clear the rows referencing the agents
// it hard-deletes and the runtime itself. These tests pin that cleanup on both
// delete entry points (strict DeleteAgentRuntime and the cascade
// ArchiveAgentsAndDeleteRuntime); without the sweep a bind_agent row would
// outlive its agent and an exec_override row would stamp new tasks with a
// dead runtime.

func countRolePolicyRowsByAgent(t *testing.T, ctx context.Context, agentID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_role_policy WHERE agent_id = $1`, agentID).Scan(&n); err != nil {
		t.Fatalf("count role policy rows by agent: %v", err)
	}
	return n
}

func countRolePolicyRowsByRuntime(t *testing.T, ctx context.Context, runtimeID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM workspace_role_policy WHERE runtime_id = $1`, runtimeID).Scan(&n); err != nil {
		t.Fatalf("count role policy rows by runtime: %v", err)
	}
	return n
}

func TestDeleteAgentRuntime_CleansRolePolicyRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	ctx := context.Background()

	runtimeID := seedIsolatedRuntime(t, "Role Policy Cleanup Runtime")
	agentID := seedAgentOnRuntime(t, runtimeID, "Role Policy Cleanup Archived Agent", true)
	seedRolePolicyRuleForTest(t, "QA", agentID, "", "", "", "", "agent_default")
	seedRolePolicyRuleForTest(t, "BE", "", runtimeID, "gpt-5", "high", "fast", "agent_default")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/"+runtimeID, nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteAgentRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if n := countRolePolicyRowsByAgent(t, ctx, agentID); n != 0 {
		t.Fatalf("bind_agent policy rows survived runtime delete: %d", n)
	}
	if n := countRolePolicyRowsByRuntime(t, ctx, runtimeID); n != 0 {
		t.Fatalf("exec_override policy rows survived runtime delete: %d", n)
	}
}

func TestArchiveAgentsAndDeleteRuntime_CleansRolePolicyRows(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Cleanup(func() { clearRolePolicyForTest(t) })
	ctx := context.Background()

	runtimeID := createCascadeFixtureRuntime(t, ctx, "Role Policy Cascade Runtime")
	agentID := createCascadeFixtureAgent(t, ctx, runtimeID, "Role Policy Cascade Agent")
	seedRolePolicyRuleForTest(t, "QA", agentID, "", "", "", "", "agent_default")
	seedRolePolicyRuleForTest(t, "BE", "", runtimeID, "gpt-5", "high", "fast", "agent_default")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/"+runtimeID+"/archive-agents-and-delete",
		map[string]any{"expected_active_agent_ids": []string{agentID}})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.ArchiveAgentsAndDeleteRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("ArchiveAgentsAndDeleteRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if n := countRolePolicyRowsByAgent(t, ctx, agentID); n != 0 {
		t.Fatalf("bind_agent policy rows survived cascade runtime delete: %d", n)
	}
	if n := countRolePolicyRowsByRuntime(t, ctx, runtimeID); n != 0 {
		t.Fatalf("exec_override policy rows survived cascade runtime delete: %d", n)
	}
}
