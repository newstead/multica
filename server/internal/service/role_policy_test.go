package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/dispatch"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// seedRolePolicyFixture builds the attribution fixture (user/workspace/member/
// runtime/assigned agent/issue), tags the assigned agent with role_code BE, and
// adds a second "policy agent" with its own runtime for bind_agent rules.
func seedRolePolicyFixture(t *testing.T, pool *pgxpool.Pool) (workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID string) {
	t.Helper()
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, issueID = seedAttributionFixture(t, pool)

	if _, err := pool.Exec(ctx, `UPDATE agent SET role_code = 'BE' WHERE id = $1`, assignedAgentID); err != nil {
		t.Fatalf("tag assigned agent role_code: %v", err)
	}

	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, owner_id)
		VALUES ($1, 'policy-runtime', 'cloud', 'codex', 'online', '', '{}'::jsonb, $2)
		RETURNING id`, workspaceID, userID).Scan(&policyRuntimeID); err != nil {
		t.Fatalf("seed policy runtime: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, policyRuntimeID) })

	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_config, runtime_id, visibility,
			max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, role_code)
		VALUES ($1, 'policy-agent', 'cloud', '{}'::jsonb, $2, 'workspace', 1, $3, '', '{}'::jsonb, '[]'::jsonb, 'QA')
		RETURNING id`, workspaceID, policyRuntimeID, userID).Scan(&policyAgentID); err != nil {
		t.Fatalf("seed policy agent: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, policyAgentID) })

	return workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID
}

func setRolePolicyEnabled(t *testing.T, pool *pgxpool.Pool, workspaceID string, enabled bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE workspace SET role_policy_enabled = $2 WHERE id = $1`, workspaceID, enabled); err != nil {
		t.Fatalf("set role_policy_enabled: %v", err)
	}
}

func insertRolePolicyRule(t *testing.T, pool *pgxpool.Pool, workspaceID, roleCode, agentID, runtimeID, model, thinkingLevel, serviceTier, fallback string) {
	t.Helper()
	ctx := context.Background()
	if fallback == "" {
		fallback = "agent_default"
	}
	var agentArg, runtimeArg any
	agentArg, runtimeArg = nil, nil
	if agentID != "" {
		agentArg = agentID
	}
	if runtimeID != "" {
		runtimeArg = runtimeID
	}
	var modelArg, thinkingArg, tierArg any
	if model != "" {
		modelArg = model
	}
	if thinkingLevel != "" {
		thinkingArg = thinkingLevel
	}
	if serviceTier != "" {
		tierArg = serviceTier
	}
	if _, err := pool.Exec(ctx, `
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
	`, workspaceID, roleCode, agentArg, runtimeArg, modelArg, thinkingArg, tierArg, fallback); err != nil {
		t.Fatalf("insert role policy rule: %v", err)
	}
}

func readRolePolicyTask(t *testing.T, pool *pgxpool.Pool, taskID pgtype.UUID) db.AgentTaskQueue {
	t.Helper()
	q := db.New(pool)
	task, err := q.GetAgentTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("read task %s: %v", util.UUIDToString(taskID), err)
	}
	return task
}

func newRolePolicyTaskSvc(pool *pgxpool.Pool) *TaskService {
	q := db.New(pool)
	return &TaskService{Queries: q, TxStarter: pool, Bus: events.New()}
}

func issueStruct(issueID, workspaceID, userID, agentID string) db.Issue {
	return db.Issue{
		ID:           util.MustParseUUID(issueID),
		AssigneeID:   util.MustParseUUID(agentID),
		Priority:     "medium",
		CreatorType:  "member",
		CreatorID:    util.MustParseUUID(userID),
		WorkspaceID:  util.MustParseUUID(workspaceID),
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
	}
}

// TestResolveRolePolicy covers the resolver matrix from ROLEPOL-0 §5:
// feature off / no role_code / no rule / bind_agent / exec_override /
// disabled / archived policy agent (degradation).
func TestResolveRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)

	workspaceID, _, assignedAgentID, policyAgentID, policyRuntimeID, _ := seedRolePolicyFixture(t, pool)

	assignedAgent, err := q.GetAgent(ctx, util.MustParseUUID(assignedAgentID))
	if err != nil {
		t.Fatalf("load assigned agent: %v", err)
	}

	t.Run("feature off", func(t *testing.T) {
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyAgentDefault {
			t.Fatalf("mode = %q, want agent_default", res.Mode)
		}
		if res.AgentID.Bytes != assignedAgent.ID.Bytes {
			t.Fatalf("agent_id changed while feature off: %v", res.AgentID)
		}
	})

	t.Run("no role_code", func(t *testing.T) {
		setRolePolicyEnabled(t, pool, workspaceID, true)
		noRole := assignedAgent
		noRole.RoleCode = pgtype.Text{}
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), noRole)
		if res.Mode != RolePolicyAgentDefault {
			t.Fatalf("mode = %q, want agent_default", res.Mode)
		}
	})

	t.Run("no rule for role", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "QA", policyAgentID, "", "", "", "", "agent_default")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyAgentDefault {
			t.Fatalf("mode = %q, want agent_default", res.Mode)
		}
	})

	t.Run("bind_agent", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyBindAgent {
			t.Fatalf("mode = %q, want bind_agent", res.Mode)
		}
		if res.AgentID.Bytes != util.MustParseUUID(policyAgentID).Bytes {
			t.Fatalf("agent_id = %s, want policy agent %s", util.UUIDToString(res.AgentID), policyAgentID)
		}
		if res.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("runtime_id = %s, want policy runtime %s", util.UUIDToString(res.RuntimeID), policyRuntimeID)
		}
		if res.PolicyModel.Valid || res.PolicyThinkingLevel.Valid || res.PolicyServiceTier.Valid {
			t.Fatalf("bind_agent must not carry exec overrides")
		}
	})

	t.Run("exec_override", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyExecOverride {
			t.Fatalf("mode = %q, want exec_override", res.Mode)
		}
		if res.AgentID.Bytes != assignedAgent.ID.Bytes {
			t.Fatalf("exec_override must keep the assigned agent, got %s", util.UUIDToString(res.AgentID))
		}
		if res.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("runtime_id = %s, want rule runtime %s", util.UUIDToString(res.RuntimeID), policyRuntimeID)
		}
		if res.PolicyModel.String != "gpt-5" || res.PolicyThinkingLevel.String != "high" || res.PolicyServiceTier.String != "fast" {
			t.Fatalf("exec overrides not carried: %+v", res)
		}
	})

	t.Run("exec_override falls back to agent runtime", func(t *testing.T) {
		// Rule with model but no runtime_id → agent's own runtime is kept.
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", "", "gpt-5", "", "", "agent_default")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyExecOverride {
			t.Fatalf("mode = %q, want exec_override", res.Mode)
		}
		if res.RuntimeID.Bytes != assignedAgent.RuntimeID.Bytes {
			t.Fatalf("runtime_id = %s, want agent runtime %s", util.UUIDToString(res.RuntimeID), util.UUIDToString(assignedAgent.RuntimeID))
		}
		if res.PolicyModel.String != "gpt-5" {
			t.Fatalf("model = %q, want gpt-5", res.PolicyModel.String)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", "", "", "", "", "disabled")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyDisabled {
			t.Fatalf("mode = %q, want disabled", res.Mode)
		}
	})

	t.Run("archived policy agent degrades", func(t *testing.T) {
		// Re-point the bind rule at a freshly archived agent.
		if _, err := pool.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = $1`, policyAgentID); err != nil {
			t.Fatalf("archive policy agent: %v", err)
		}
		t.Cleanup(func() {
			pool.Exec(context.Background(), `UPDATE agent SET archived_at = NULL WHERE id = $1`, policyAgentID)
		})
		insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")
		svc := newRolePolicyTaskSvc(pool)
		res := svc.ResolveRolePolicy(ctx, util.MustParseUUID(workspaceID), assignedAgent)
		if res.Mode != RolePolicyAgentDefault {
			t.Fatalf("mode = %q, want agent_default (degraded)", res.Mode)
		}
		if res.AgentID.Bytes != assignedAgent.ID.Bytes {
			t.Fatalf("degraded resolution must keep the assigned agent")
		}
	})
}

// TestEnqueueTaskForIssueAppliesRolePolicy verifies the issue-assignment path
// stamps the resolved executor / exec overrides, and refuses disabled roles.
func TestEnqueueTaskForIssueAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)

	t.Run("bind_agent stamps policy agent", func(t *testing.T) {
		setRolePolicyEnabled(t, pool, workspaceID, true)
		insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")
		task, err := svc.EnqueueTaskForIssue(ctx, issue)
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.AgentID.Bytes != util.MustParseUUID(policyAgentID).Bytes {
			t.Fatalf("task agent_id = %s, want policy agent %s", util.UUIDToString(row.AgentID), policyAgentID)
		}
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want policy runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.PolicyModel.Valid || row.PolicyThinkingLevel.Valid || row.PolicyServiceTier.Valid {
			t.Fatalf("bind_agent task must not carry policy columns")
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("exec_override stamps policy columns", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")
		task, err := svc.EnqueueTaskForIssue(ctx, issue)
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.AgentID.Bytes != util.MustParseUUID(assignedAgentID).Bytes {
			t.Fatalf("task agent_id = %s, want assigned agent %s", util.UUIDToString(row.AgentID), assignedAgentID)
		}
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.PolicyModel.String != "gpt-5" || row.PolicyThinkingLevel.String != "high" || row.PolicyServiceTier.String != "fast" {
			t.Fatalf("policy columns not stamped: %+v", row)
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("disabled role refuses enqueue", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", "", "", "", "", "disabled")
		before := countTasksForIssue(t, pool, issueID)
		_, err := svc.EnqueueTaskForIssue(ctx, issue)
		if !errors.Is(err, ErrRolePolicyDisabled) {
			t.Fatalf("EnqueueTaskForIssue: err = %v, want ErrRolePolicyDisabled", err)
		}
		after := countTasksForIssue(t, pool, issueID)
		if after != before {
			t.Fatalf("disabled role must not create a task: before=%d after=%d", before, after)
		}
	})
}

// TestEnqueueTaskForMentionAppliesRolePolicy covers the mention path
// (thread-parent / squad-leader reuse the same enqueueMentionTaskWithCommentPlan).
func TestEnqueueTaskForMentionAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, _, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)

	setRolePolicyEnabled(t, pool, workspaceID, true)
	insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "", "agent_default")
	task, err := svc.EnqueueTaskForMention(ctx, issue, util.MustParseUUID(assignedAgentID), pgtype.UUID{})
	if err != nil {
		t.Fatalf("EnqueueTaskForMention: %v", err)
	}
	row := readRolePolicyTask(t, pool, task.ID)
	if row.AgentID.Bytes != util.MustParseUUID(assignedAgentID).Bytes {
		t.Fatalf("task agent_id = %s, want assigned agent %s", util.UUIDToString(row.AgentID), assignedAgentID)
	}
	if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
		t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
	}
	if row.PolicyModel.String != "gpt-5" || row.PolicyThinkingLevel.String != "high" {
		t.Fatalf("mention policy columns not stamped: %+v", row)
	}
	assertPolicyRoleCode(t, row, "BE")
}

// TestEnqueueDeferredAssigneeFallbackAppliesRolePolicy covers the deferred
// fallback path (comment-routing escalation).
func TestEnqueueDeferredAssigneeFallbackAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, _, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)

	setRolePolicyEnabled(t, pool, workspaceID, true)
	insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "", "fast", "agent_default")
	task, err := svc.EnqueueDeferredAssigneeFallback(ctx, issue, util.MustParseUUID(assignedAgentID), pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("EnqueueDeferredAssigneeFallback: %v", err)
	}
	row := readRolePolicyTask(t, pool, task.ID)
	if row.Status != "deferred" {
		t.Fatalf("status = %q, want deferred", row.Status)
	}
	if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
		t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
	}
	if row.PolicyModel.String != "gpt-5" || row.PolicyServiceTier.String != "fast" {
		t.Fatalf("deferred policy columns not stamped: %+v", row)
	}
	assertPolicyRoleCode(t, row, "BE")
}

// TestDispatchRunOnlyAppliesRolePolicy covers the autopilot path: exec_override
// stamps the task, disabled roles skip dispatch with ReasonRolePolicyBlocked.
func TestDispatchRunOnlyAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	q := db.New(pool)
	workspaceID, publisherID, assignedAgentID, _, policyRuntimeID, _ := seedRolePolicyFixture(t, pool)
	autopilotID, runID := seedRunOnlyAutopilot(t, pool, workspaceID, assignedAgentID, publisherID)
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE autopilot_run_id = $1`, runID)
	})

	svc := &AutopilotService{Queries: q, TxStarter: pool, Bus: events.New(), TaskSvc: newRolePolicyTaskSvc(pool)}
	ap, err := q.GetAutopilot(ctx, util.MustParseUUID(autopilotID))
	if err != nil {
		t.Fatalf("get autopilot: %v", err)
	}
	run, err := q.GetAutopilotRun(ctx, util.MustParseUUID(runID))
	if err != nil {
		t.Fatalf("get run: %v", err)
	}

	t.Run("exec_override stamps autopilot task", func(t *testing.T) {
		setRolePolicyEnabled(t, pool, workspaceID, true)
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "", "", "agent_default")
		if err := svc.dispatchRunOnly(ctx, ap, &run, pgtype.UUID{}); err != nil {
			t.Fatalf("dispatchRunOnly: %v", err)
		}
		var taskID pgtype.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM agent_task_queue WHERE autopilot_run_id = $1`, run.ID).Scan(&taskID); err != nil {
			t.Fatalf("read autopilot task: %v", err)
		}
		row := readRolePolicyTask(t, pool, taskID)
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.PolicyModel.String != "gpt-5" {
			t.Fatalf("autopilot policy_model not stamped: %+v", row)
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("disabled role skips dispatch", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", "", "", "", "", "disabled")
		before := countTasksForRun(t, pool, runID)
		err := svc.dispatchRunOnly(ctx, ap, &run, pgtype.UUID{})
		var skipped *errDispatchSkipped
		if !errors.As(err, &skipped) {
			t.Fatalf("dispatchRunOnly: err = %v, want errDispatchSkipped", err)
		}
		if skipped.code != dispatch.ReasonRolePolicyBlocked {
			t.Fatalf("skip code = %q, want %q", skipped.code, dispatch.ReasonRolePolicyBlocked)
		}
		after := countTasksForRun(t, pool, runID)
		if after != before {
			t.Fatalf("disabled role must not create a task: before=%d after=%d", before, after)
		}
	})
}

func countTasksForIssue(t *testing.T, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE issue_id = $1`, issueID).Scan(&n); err != nil {
		t.Fatalf("count tasks for issue: %v", err)
	}
	return n
}

func countTasksForRun(t *testing.T, pool *pgxpool.Pool, runID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM agent_task_queue WHERE autopilot_run_id = $1`, runID).Scan(&n); err != nil {
		t.Fatalf("count tasks for run: %v", err)
	}
	return n
}

// assertPolicyRoleCode fails the test unless the task row carries the expected
// policy_role_code audit value ("" means the column must be NULL / invalid).
func assertPolicyRoleCode(t *testing.T, row db.AgentTaskQueue, want string) {
	t.Helper()
	if want == "" {
		if row.PolicyRoleCode.Valid {
			t.Fatalf("policy_role_code = %q, want NULL", row.PolicyRoleCode.String)
		}
		return
	}
	if !row.PolicyRoleCode.Valid || row.PolicyRoleCode.String != want {
		t.Fatalf("policy_role_code = %v, want %q", row.PolicyRoleCode, want)
	}
}

// TestEnqueueTaskForSquadLeaderAppliesRolePolicy covers the squad-leader
// routing boundary (ROLEPOL-3): both EnqueueTaskForSquadLeader and
// EnqueueTaskForSquadLeaderWithHandoff flow through the mention enqueue path
// and must stamp the policy executor / exec overrides / audit role_code while
// preserving the is_leader_task and squad_id markers the claim handler needs.
func TestEnqueueTaskForSquadLeaderAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)

	var squadID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO squad (workspace_id, name, leader_id, creator_id)
		VALUES ($1, 'rol-211-squad', $2, $3)
		RETURNING id`, workspaceID, assignedAgentID, userID).Scan(&squadID); err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM squad WHERE id = $1`, squadID) })

	svc := newRolePolicyTaskSvc(pool)
	setRolePolicyEnabled(t, pool, workspaceID, true)

	t.Run("squad leader exec_override", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")
		task, err := svc.EnqueueTaskForSquadLeader(ctx, issue, util.MustParseUUID(assignedAgentID), util.MustParseUUID(squadID), pgtype.UUID{})
		if err != nil {
			t.Fatalf("EnqueueTaskForSquadLeader: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if !row.IsLeaderTask {
			t.Fatal("squad leader task must carry is_leader_task=true")
		}
		if row.SquadID.Bytes != util.MustParseUUID(squadID).Bytes {
			t.Fatalf("task squad_id = %s, want %s", util.UUIDToString(row.SquadID), squadID)
		}
		if row.AgentID.Bytes != util.MustParseUUID(assignedAgentID).Bytes {
			t.Fatalf("task agent_id = %s, want assigned agent %s", util.UUIDToString(row.AgentID), assignedAgentID)
		}
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.PolicyModel.String != "gpt-5" || row.PolicyThinkingLevel.String != "high" || row.PolicyServiceTier.String != "fast" {
			t.Fatalf("squad leader policy columns not stamped: %+v", row)
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("squad leader handoff bind_agent", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")
		task, err := svc.EnqueueTaskForSquadLeaderWithHandoff(ctx, issue, util.MustParseUUID(assignedAgentID), util.MustParseUUID(squadID), "handoff note", util.MustParseUUID(userID))
		if err != nil {
			t.Fatalf("EnqueueTaskForSquadLeaderWithHandoff: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if !row.IsLeaderTask {
			t.Fatal("squad leader handoff task must carry is_leader_task=true")
		}
		if row.SquadID.Bytes != util.MustParseUUID(squadID).Bytes {
			t.Fatalf("task squad_id = %s, want %s", util.UUIDToString(row.SquadID), squadID)
		}
		if row.AgentID.Bytes != util.MustParseUUID(policyAgentID).Bytes {
			t.Fatalf("task agent_id = %s, want policy agent %s", util.UUIDToString(row.AgentID), policyAgentID)
		}
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want policy runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.HandoffNote.String != "handoff note" {
			t.Fatalf("handoff note not stamped: %+v", row)
		}
		assertPolicyRoleCode(t, row, "BE")
	})
}

// TestRolePolicyNoCrossWorkspaceLeakage pins ROLEPOL-3 acceptance: a rule in
// workspace A must never affect a task in workspace B (and vice versa). Both
// workspaces have the same role_code enabled with DIFFERENT rules, so any
// leak would surface as a wrong model/runtime on the other workspace's task.
func TestRolePolicyNoCrossWorkspaceLeakage(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()

	wsA, userA, agentA, _, runtimeA, issueA := seedRolePolicyFixture(t, pool)
	wsB, userB, agentB, issueB := seedAttributionFixture(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE agent SET role_code = 'BE' WHERE id = $1`, agentB); err != nil {
		t.Fatalf("tag workspace B agent role_code: %v", err)
	}
	var runtimeB string
	if err := pool.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = $1`, agentB).Scan(&runtimeB); err != nil {
		t.Fatalf("load workspace B agent runtime: %v", err)
	}
	issueBStruct := issueStruct(issueB, wsB, userB, agentB)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueB) })
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueA) })

	svc := newRolePolicyTaskSvc(pool)
	setRolePolicyEnabled(t, pool, wsA, true)
	insertRolePolicyRule(t, pool, wsA, "BE", "", runtimeA, "gpt-5", "high", "fast", "agent_default")
	setRolePolicyEnabled(t, pool, wsB, true)
	insertRolePolicyRule(t, pool, wsB, "BE", "", runtimeB, "gpt-6", "medium", "default", "agent_default")

	t.Run("workspace A task uses workspace A rule", func(t *testing.T) {
		task, err := svc.EnqueueTaskForIssue(ctx, issueStruct(issueA, wsA, userA, agentA))
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue (A): %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.RuntimeID.Bytes != util.MustParseUUID(runtimeA).Bytes {
			t.Fatalf("task A runtime = %s, want workspace A rule runtime %s", util.UUIDToString(row.RuntimeID), runtimeA)
		}
		if row.PolicyModel.String != "gpt-5" {
			t.Fatalf("task A policy_model = %q, want gpt-5 (workspace A rule)", row.PolicyModel.String)
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("workspace B task uses workspace B rule", func(t *testing.T) {
		task, err := svc.EnqueueTaskForIssue(ctx, issueBStruct)
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue (B): %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.RuntimeID.Bytes != util.MustParseUUID(runtimeB).Bytes {
			t.Fatalf("task B runtime = %s, want workspace B rule runtime %s", util.UUIDToString(row.RuntimeID), runtimeB)
		}
		if row.PolicyModel.String != "gpt-6" {
			t.Fatalf("task B policy_model = %q, want gpt-6 (workspace B rule)", row.PolicyModel.String)
		}
		assertPolicyRoleCode(t, row, "BE")
	})
}

// TestRolePolicyOfflineRuntimeTarget pins the offline-target handling
// (ROLEPOL-3 acceptance): a policy rule may point at an agent/runtime that is
// currently offline. Offline is a transient condition (unlike archived), so
// the task is still enqueued with the resolved config and simply waits for the
// runtime to come back — the queue/daemon receive the effective
// agent/runtime/model/thinking_level/service_tier predictably.
func TestRolePolicyOfflineRuntimeTarget(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)
	setRolePolicyEnabled(t, pool, workspaceID, true)

	if _, err := pool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, policyRuntimeID); err != nil {
		t.Fatalf("set policy runtime offline: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `UPDATE agent_runtime SET status = 'online' WHERE id = $1`, policyRuntimeID)
	})

	t.Run("bind_agent to offline runtime still routes", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")
		task, err := svc.EnqueueTaskForIssue(ctx, issue)
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.AgentID.Bytes != util.MustParseUUID(policyAgentID).Bytes {
			t.Fatalf("task agent_id = %s, want policy agent %s", util.UUIDToString(row.AgentID), policyAgentID)
		}
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want policy runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		assertPolicyRoleCode(t, row, "BE")
	})

	t.Run("exec_override to offline runtime still routes", func(t *testing.T) {
		insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")
		task, err := svc.EnqueueTaskForIssue(ctx, issue)
		if err != nil {
			t.Fatalf("EnqueueTaskForIssue: %v", err)
		}
		row := readRolePolicyTask(t, pool, task.ID)
		if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
			t.Fatalf("task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
		}
		if row.PolicyModel.String != "gpt-5" {
			t.Fatalf("task policy_model = %q, want gpt-5", row.PolicyModel.String)
		}
		assertPolicyRoleCode(t, row, "BE")
	})
}

// TestRolePolicyPrivateTargetBind covers private-target handling (ROLEPOL-3
// acceptance): a policy rule may bind a private (owner-only) agent. The policy
// PUT is owner/admin-authorized, so enqueue is allowed even though a plain
// member mention of that agent would be denied by the private-agent gate — the
// admin-configured policy IS the authorization (mirrors the A2A handoff carve
// out in the assign path).
func TestRolePolicyPrivateTargetBind(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, policyAgentID, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)
	setRolePolicyEnabled(t, pool, workspaceID, true)

	if _, err := pool.Exec(ctx, `UPDATE agent SET permission_mode = 'private', owner_id = $2 WHERE id = $1`, policyAgentID, userID); err != nil {
		t.Fatalf("make policy agent private: %v", err)
	}
	insertRolePolicyRule(t, pool, workspaceID, "BE", policyAgentID, "", "", "", "", "agent_default")

	task, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatalf("EnqueueTaskForIssue: %v", err)
	}
	row := readRolePolicyTask(t, pool, task.ID)
	if row.AgentID.Bytes != util.MustParseUUID(policyAgentID).Bytes {
		t.Fatalf("task agent_id = %s, want private policy agent %s", util.UUIDToString(row.AgentID), policyAgentID)
	}
	if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
		t.Fatalf("task runtime_id = %s, want policy runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
	}
	assertPolicyRoleCode(t, row, "BE")
}

// TestIssueCreateAgentCreatedSubissueAppliesRolePolicy covers the
// agent-created subissue boundary (ROLEPOL-3): an agent creating a sub-issue
// with an agent assignee routes through IssueService.Create →
// maybeEnqueueOnAssign → EnqueueTaskForIssue, so the workspace role policy
// must stamp the sub-issue's task exactly like a member-created assignment.
func TestIssueCreateAgentCreatedSubissueAppliesRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, _, assignedAgentID, _, policyRuntimeID, parentIssueID := seedRolePolicyFixture(t, pool)
	setRolePolicyEnabled(t, pool, workspaceID, true)
	insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")

	q := db.New(pool)
	svc := newRolePolicyTaskSvc(pool)
	isvc := &IssueService{Queries: q, TxStarter: pool, Bus: events.New(), TaskService: svc}

	title := fmt.Sprintf("rol-211 agent-created subissue %d", time.Now().UnixNano())
	res, err := isvc.Create(ctx, IssueCreateParams{
		WorkspaceID:   util.MustParseUUID(workspaceID),
		Title:         title,
		Status:        "todo",
		Priority:      "medium",
		AssigneeType:  pgtype.Text{String: "agent", Valid: true},
		AssigneeID:    util.MustParseUUID(assignedAgentID),
		CreatorType:   "agent",
		CreatorID:     util.MustParseUUID(assignedAgentID),
		ParentIssueID: util.MustParseUUID(parentIssueID),
		OriginType:    pgtype.Text{String: "agent_create", Valid: true},
		OriginID:      util.MustParseUUID(parentIssueID),
	}, IssueCreateOpts{})
	if err != nil {
		t.Fatalf("IssueService.Create: %v", err)
	}
	subID := res.Issue.ID
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, subID) })

	var taskID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM agent_task_queue WHERE issue_id = $1`, subID).Scan(&taskID); err != nil {
		t.Fatalf("subissue task not enqueued: %v", err)
	}
	row := readRolePolicyTask(t, pool, taskID)
	if row.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
		t.Fatalf("subissue task runtime_id = %s, want rule runtime %s", util.UUIDToString(row.RuntimeID), policyRuntimeID)
	}
	if row.PolicyModel.String != "gpt-5" || row.PolicyThinkingLevel.String != "high" || row.PolicyServiceTier.String != "fast" {
		t.Fatalf("subissue policy columns not stamped: %+v", row)
	}
	assertPolicyRoleCode(t, row, "BE")
}

// TestCreateRetryTaskInheritsRolePolicy pins the retry boundary (ROLEPOL-3):
// a retry of a policy-routed task must inherit the parent's policy columns
// (policy_model/thinking_level/service_tier AND policy_role_code) so the
// re-attempt executes with the SAME effective config as the original run —
// the queue/daemon never silently falls back to the agent's own config on
// retry.
func TestCreateRetryTaskInheritsRolePolicy(t *testing.T) {
	pool := newResolveOriginatorPool(t)
	ctx := context.Background()
	workspaceID, userID, assignedAgentID, _, policyRuntimeID, issueID := seedRolePolicyFixture(t, pool)
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID) })
	issue := issueStruct(issueID, workspaceID, userID, assignedAgentID)
	svc := newRolePolicyTaskSvc(pool)
	setRolePolicyEnabled(t, pool, workspaceID, true)
	insertRolePolicyRule(t, pool, workspaceID, "BE", "", policyRuntimeID, "gpt-5", "high", "fast", "agent_default")

	parent, err := svc.EnqueueTaskForIssue(ctx, issue)
	if err != nil {
		t.Fatalf("EnqueueTaskForIssue: %v", err)
	}
	parentRow := readRolePolicyTask(t, pool, parent.ID)
	assertPolicyRoleCode(t, parentRow, "BE")

	// A retry only happens after the parent attempt has failed and left the
	// queued window; otherwise the partial unique index
	// idx_one_pending_task_per_issue_agent rejects the child.
	if _, err := pool.Exec(ctx, `UPDATE agent_task_queue SET status = 'failed' WHERE id = $1`, parent.ID); err != nil {
		t.Fatalf("fail parent task: %v", err)
	}
	child, err := db.New(pool).CreateRetryTask(ctx, db.CreateRetryTaskParams{ID: parent.ID})
	if err != nil {
		t.Fatalf("CreateRetryTask: %v", err)
	}
	childRow := readRolePolicyTask(t, pool, child.ID)
	if childRow.RuntimeID.Bytes != util.MustParseUUID(policyRuntimeID).Bytes {
		t.Fatalf("retry runtime_id = %s, want policy runtime %s", util.UUIDToString(childRow.RuntimeID), policyRuntimeID)
	}
	if childRow.PolicyModel.String != "gpt-5" || childRow.PolicyThinkingLevel.String != "high" || childRow.PolicyServiceTier.String != "fast" {
		t.Fatalf("retry policy columns not inherited: %+v", childRow)
	}
	assertPolicyRoleCode(t, childRow, "BE")
	if childRow.RetryOfTaskID.Bytes != parent.ID.Bytes {
		t.Fatalf("retry_of_task_id = %s, want %s", util.UUIDToString(childRow.RetryOfTaskID), util.UUIDToString(parent.ID))
	}
}
