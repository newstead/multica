package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func createOpsMetricsFixture(t *testing.T, ctx context.Context) (workspaceID string) {
	t.Helper()
	pool := newTaskClaimRacePool(t)
	suffix := time.Now().UnixNano()

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Ops Metrics Test", fmt.Sprintf("ops-metrics-%d@multica.ai", suffix)).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Ops Metrics Test", fmt.Sprintf("ops-metrics-%d", suffix), "ops metrics fixture", "OPS").Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, owner_id, last_seen_at)
		VALUES ($1, 'ops-daemon', 'Ops Runtime', 'cloud', 'ops_provider', 'online', 'ops runtime', '{}'::jsonb, $2, now())
		RETURNING id
	`, workspaceID, userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	var agentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, description, runtime_mode, runtime_config, runtime_id, visibility, permission_mode, max_concurrent_tasks, owner_id)
		VALUES ($1, 'Ops Agent', '', 'cloud', '{}'::jsonb, $2, 'private', 'private', 3, $3)
		RETURNING id
	`, workspaceID, runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	var blockedIssueID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position, metadata)
		VALUES ($1, 'Blocked ops issue', 'blocked', 'high', $2, 'member', 41, 1, '{"blocked_reason":"Need VPN","waiting_on":"ops"}'::jsonb)
		RETURNING id
	`, workspaceID, userID).Scan(&blockedIssueID); err != nil {
		t.Fatalf("create blocked issue: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, 'In progress issue', 'in_progress', 'medium', $2, 'member', 42, 2)
	`, workspaceID, userID); err != nil {
		t.Fatalf("create in-progress issue: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority, context, started_at)
		VALUES ($1, $2, $3, 'running', 1, '{}'::jsonb, now())
	`, agentID, runtimeID, blockedIssueID); err != nil {
		t.Fatalf("create running task: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		pool.Exec(c, `DELETE FROM agent_task_queue WHERE agent_id = $1`, agentID)
		pool.Exec(c, `DELETE FROM issue WHERE workspace_id = $1`, workspaceID)
		pool.Exec(c, `DELETE FROM agent WHERE id = $1`, agentID)
		pool.Exec(c, `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		pool.Exec(c, `DELETE FROM member WHERE workspace_id = $1 AND user_id = $2`, workspaceID, userID)
		pool.Exec(c, `DELETE FROM workspace WHERE id = $1`, workspaceID)
		pool.Exec(c, `DELETE FROM "user" WHERE id = $1`, userID)
	})

	return workspaceID
}

func TestOpsMetricsSummaryAggregatesWorkspaceState(t *testing.T) {
	ctx := context.Background()
	workspaceID := createOpsMetricsFixture(t, ctx)
	pool := newTaskClaimRacePool(t)
	svc := NewOpsMetricsService(db.New(pool))

	got, err := svc.Summary(ctx, util.MustParseUUID(workspaceID), time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}

	if got.GeneratedAt != "2026-08-03T12:00:00Z" || got.Server.Status != "ok" {
		t.Fatalf("server fields mismatch: %+v", got)
	}
	if got.IssueCounts.Blocked != 1 || got.IssueCounts.InProgress != 1 {
		t.Fatalf("issue counts = %+v, want blocked=1 in_progress=1", got.IssueCounts)
	}
	if got.RuntimeHealth.Total != 1 || got.RuntimeHealth.Online != 1 || got.RuntimeHealth.LastSeenAt == nil {
		t.Fatalf("runtime health = %+v, want one online runtime with last_seen_at", got.RuntimeHealth)
	}
	if got.AgentCapacity.TotalAgents != 1 || got.AgentCapacity.ActiveAgents != 1 || got.AgentCapacity.TotalSlots != 3 || got.AgentCapacity.ActiveSlots != 1 || got.AgentCapacity.IdleSlots != 2 {
		t.Fatalf("capacity = %+v, want one active agent using one of three slots", got.AgentCapacity)
	}
	if got.ActiveTaskCounts.Running != 1 || len(got.ActiveTasks) != 1 {
		t.Fatalf("active tasks = counts %+v list %+v, want one running task", got.ActiveTaskCounts, got.ActiveTasks)
	}
	if got.ActiveTasks[0].IssueIdentifier == nil || *got.ActiveTasks[0].IssueIdentifier != "OPS-41" {
		t.Fatalf("active task issue identifier = %v, want OPS-41", got.ActiveTasks[0].IssueIdentifier)
	}
	if len(got.RecentBlockers) != 1 || got.RecentBlockers[0].BlockedReason == nil || *got.RecentBlockers[0].BlockedReason != "Need VPN" {
		t.Fatalf("recent blockers = %+v, want blocked reason", got.RecentBlockers)
	}
}
