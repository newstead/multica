package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetOpsMetricsRequiresWorkspaceMember(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ops/metrics", nil)
	req.Header.Set("X-User-ID", testUserID)
	w := httptest.NewRecorder()

	testHandler.GetOpsMetrics(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GetOpsMetrics without workspace: expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestGetOpsMetricsReturnsWorkspaceScopedSummary(t *testing.T) {
	ctx := t.Context()
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, metadata)
		VALUES ($1, 'Handler blocked ops issue', 'blocked', 'urgent', $2, 'member', 910001, -910001, '{"blocked_reason":"Approval needed"}'::jsonb)
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create blocked issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	var agentID string
	if err := testPool.QueryRow(ctx, `SELECT id FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load test agent: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, context, started_at)
		VALUES ($1, $2, $3, 'running', 1, '{}'::jsonb, now())
	`, agentID, testRuntimeID, issueID); err != nil {
		t.Fatalf("create running task: %v", err)
	}

	w := httptest.NewRecorder()
	testHandler.GetOpsMetrics(w, newRequest(http.MethodGet, "/api/ops/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GetOpsMetrics: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Server struct {
			Status string `json:"status"`
		} `json:"server"`
		IssueCounts struct {
			Blocked int64 `json:"blocked"`
		} `json:"issue_counts"`
		ActiveTaskCounts struct {
			Running int64 `json:"running"`
		} `json:"active_task_counts"`
		RecentBlockers []struct {
			Identifier    string  `json:"identifier"`
			BlockedReason *string `json:"blocked_reason"`
		} `json:"recent_blockers"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Server.Status != "ok" {
		t.Fatalf("server status = %q, want ok", resp.Server.Status)
	}
	if resp.IssueCounts.Blocked < 1 || resp.ActiveTaskCounts.Running < 1 {
		t.Fatalf("counts = issue %+v task %+v, want blocked/running present", resp.IssueCounts, resp.ActiveTaskCounts)
	}
	if len(resp.RecentBlockers) == 0 || resp.RecentBlockers[0].BlockedReason == nil {
		t.Fatalf("recent blockers missing reason: %+v", resp.RecentBlockers)
	}
}
