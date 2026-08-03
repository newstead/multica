package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// TestClaimTaskByRuntime_RolePolicyOverrides pins ROLEPOL-0 §6.2: a task with
// policy_* columns carries those exec overrides in TaskAgentData (winning over
// the agent's own config); a task without them carries the agent values.
func TestClaimTaskByRuntime_RolePolicyOverrides(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "RolePolicyClaim-"+uuid.NewString()[:8], []byte("[]"))
	runtimeID := handlerTestRuntimeID(t)

	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET model = $2, thinking_level = $3, service_tier = $4 WHERE id = $1
	`, agentID, "agent-base-model", "medium", "default"); err != nil {
		t.Fatalf("set agent exec config: %v", err)
	}

	insertClaimTask := func(st *testing.T, policyModel, policyThinking, policyTier, policyRoleCode string) string {
		st.Helper()
		issueID := createLanguagePolicyClaimIssue(st, "")
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority,
				policy_model, policy_thinking_level, policy_service_tier, policy_role_code)
			VALUES ($1, $2, $3, 'queued', 1000, $4, $5, $6, $7)
			RETURNING id
		`, agentID, runtimeID, issueID, nullableString(policyModel), nullableString(policyThinking), nullableString(policyTier), nullableString(policyRoleCode)).Scan(&taskID); err != nil {
			st.Fatalf("create claim task: %v", err)
		}
		st.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
		return taskID
	}

	claim := func(st *testing.T) agentClaimShape {
		st.Helper()
		w := httptest.NewRecorder()
		req := newDaemonTokenRequest(
			http.MethodPost,
			"/api/daemon/runtimes/"+runtimeID+"/claim",
			nil,
			testWorkspaceID,
			"role-policy-claim-test",
		)
		req = withURLParam(req, "runtimeId", runtimeID)
		testHandler.ClaimTaskByRuntime(w, req)
		if w.Code != http.StatusOK {
			st.Fatalf("ClaimTaskByRuntime: expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Task *struct {
				Agent *struct {
					Model          string `json:"model"`
					ThinkingLevel  string `json:"thinking_level"`
					ServiceTier    string `json:"service_tier"`
					PolicyRoleCode string `json:"policy_role_code"`
				} `json:"agent"`
			} `json:"task"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			st.Fatalf("decode claim response: %v", err)
		}
		if resp.Task == nil || resp.Task.Agent == nil {
			st.Fatal("claim response missing agent data")
		}
		return agentClaimShape{
			model:          resp.Task.Agent.Model,
			thinkingLevel:  resp.Task.Agent.ThinkingLevel,
			serviceTier:    resp.Task.Agent.ServiceTier,
			policyRoleCode: resp.Task.Agent.PolicyRoleCode,
		}
	}

	t.Run("no policy columns uses agent config", func(t *testing.T) {
		insertClaimTask(t, "", "", "", "")
		got := claim(t)
		if got.model != "agent-base-model" || got.thinkingLevel != "medium" || got.serviceTier != "default" {
			t.Fatalf("agent config not preserved: %+v", got)
		}
		if got.policyRoleCode != "" {
			t.Fatalf("policy_role_code = %q, want empty", got.policyRoleCode)
		}
	})

	t.Run("policy columns override agent config", func(t *testing.T) {
		insertClaimTask(t, "policy-model", "high", "fast", "QA")
		got := claim(t)
		if got.model != "policy-model" || got.thinkingLevel != "high" || got.serviceTier != "fast" {
			t.Fatalf("policy overrides not applied: %+v", got)
		}
		if got.policyRoleCode != "QA" {
			t.Fatalf("policy_role_code = %q, want QA", got.policyRoleCode)
		}
	})
}

type agentClaimShape struct {
	model          string
	thinkingLevel  string
	serviceTier    string
	policyRoleCode string
}
