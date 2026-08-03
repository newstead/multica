package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Role policy (ROLEPOL-0 / ROL-214): a per-workspace matrix binding agent
// roles (role_code) to concrete AI execution configs. Workspace routing
// applies it to NEW tasks only; already enqueued/running tasks are never
// rewritten. When the feature is off, the role has no rule, or the agent has
// no role_code, the historical agent-centric behavior is preserved unchanged.

// RolePolicyMode is the outcome of resolving a workspace role policy for a
// task at enqueue time.
type RolePolicyMode string

const (
	// RolePolicyAgentDefault keeps the current agent-centric behavior: the
	// task runs as the assigned agent with its own runtime/model config.
	RolePolicyAgentDefault RolePolicyMode = "agent_default"
	// RolePolicyBindAgent binds the task to the policy agent: task.agent_id
	// becomes the policy agent and task.runtime_id its runtime.
	RolePolicyBindAgent RolePolicyMode = "bind_agent"
	// RolePolicyExecOverride keeps the assigned agent and layers exec
	// overrides (runtime_id, model, thinking_level, service_tier) on top.
	RolePolicyExecOverride RolePolicyMode = "exec_override"
	// RolePolicyDisabled refuses enqueue (fail-closed, mirroring the
	// attribution fail-closed policy): the role's rule carries
	// fallback = 'disabled'.
	RolePolicyDisabled RolePolicyMode = "disabled"
)

// RolePolicyResolution is the outcome of ResolveRolePolicy. AgentID /
// RuntimeID carry the values that must be stamped on the new task row;
// PolicyModel / PolicyThinkingLevel / PolicyServiceTier are the exec
// overrides (only set in exec_override mode; bind_agent reuses the agent's
// own config and keeps these empty).
type RolePolicyResolution struct {
	Mode                RolePolicyMode
	RoleCode            string
	AgentID             pgtype.UUID
	RuntimeID           pgtype.UUID
	PolicyModel         pgtype.Text
	PolicyThinkingLevel pgtype.Text
	PolicyServiceTier   pgtype.Text
}

// ErrRolePolicyDisabled signals that enqueue was refused because the
// workspace role policy marks the agent's role with fallback = 'disabled'
// (fail-closed). Callers map it to a clear user-facing outcome, mirroring
// ErrAttributionFailClosed handling.
var ErrRolePolicyDisabled = errors.New("role policy: enqueue refused (role disabled by workspace policy)")

// ResolveRolePolicy resolves the workspace role policy for a task about to be
// enqueued for agent. The resolution is applied ONLY at enqueue; it never
// rewrites already queued/running tasks and never changes attribution or
// authorization (originator/accountable stay exactly as the caller resolves
// them).
//
// Degradation contract: any read failure (workspace toggle, policy matrix,
// bound policy agent) degrades to agent_default with a warning log rather
// than failing the enqueue — the feature must never break existing routing on
// a transient DB hiccup. The only fail-closed outcome is an explicit
// fallback = 'disabled' rule.
func (s *TaskService) ResolveRolePolicy(ctx context.Context, workspaceID pgtype.UUID, agent db.Agent) RolePolicyResolution {
	res := RolePolicyResolution{
		Mode:      RolePolicyAgentDefault,
		AgentID:   agent.ID,
		RuntimeID: agent.RuntimeID,
	}
	if s == nil || s.Queries == nil || !workspaceID.Valid {
		return res
	}

	enabled, err := s.Queries.GetWorkspaceRolePolicyEnabled(ctx, workspaceID)
	if err != nil {
		slog.Warn("role policy: workspace toggle read failed; degrading to agent_default",
			"workspace_id", util.UUIDToString(workspaceID),
			"agent_id", util.UUIDToString(agent.ID),
			"error", err,
		)
		return res
	}
	if !enabled {
		return res
	}

	roleCode := ""
	if agent.RoleCode.Valid {
		roleCode = strings.ToUpper(strings.TrimSpace(agent.RoleCode.String))
	}
	if roleCode == "" {
		return res
	}

	rules, err := s.Queries.ListWorkspaceRolePolicies(ctx, workspaceID)
	if err != nil {
		slog.Warn("role policy: policy matrix read failed; degrading to agent_default",
			"workspace_id", util.UUIDToString(workspaceID),
			"agent_id", util.UUIDToString(agent.ID),
			"role_code", roleCode,
			"error", err,
		)
		return res
	}

	var rule *db.WorkspaceRolePolicy
	for i := range rules {
		if rules[i].RoleCode == roleCode {
			rule = &rules[i]
			break
		}
	}
	if rule == nil {
		return res
	}
	res.RoleCode = roleCode

	if rule.Fallback == "disabled" {
		res.Mode = RolePolicyDisabled
		return res
	}

	if rule.AgentID.Valid {
		// bind_agent: run as the policy agent. An archived or runtime-less
		// policy agent degrades to agent_default (warn, not fail).
		policyAgent, err := s.Queries.GetAgent(ctx, rule.AgentID)
		if err != nil {
			slog.Warn("role policy: bound policy agent read failed; degrading to agent_default",
				"workspace_id", util.UUIDToString(workspaceID),
				"assigned_agent_id", util.UUIDToString(agent.ID),
				"policy_agent_id", util.UUIDToString(rule.AgentID),
				"role_code", roleCode,
				"error", err,
			)
			return res
		}
		if policyAgent.ArchivedAt.Valid || !policyAgent.RuntimeID.Valid {
			slog.Warn("role policy: bound policy agent archived or has no runtime; degrading to agent_default",
				"workspace_id", util.UUIDToString(workspaceID),
				"assigned_agent_id", util.UUIDToString(agent.ID),
				"policy_agent_id", util.UUIDToString(rule.AgentID),
				"role_code", roleCode,
				"archived", policyAgent.ArchivedAt.Valid,
				"has_runtime", policyAgent.RuntimeID.Valid,
			)
			return res
		}
		res.Mode = RolePolicyBindAgent
		res.AgentID = policyAgent.ID
		res.RuntimeID = policyAgent.RuntimeID
		res.PolicyModel = pgtype.Text{}
		res.PolicyThinkingLevel = pgtype.Text{}
		res.PolicyServiceTier = pgtype.Text{}
		return res
	}

	// exec_override: keep the assigned agent, override runtime and exec
	// fields. runtime_id falls back to the agent's own runtime when unset.
	res.Mode = RolePolicyExecOverride
	if rule.RuntimeID.Valid {
		res.RuntimeID = rule.RuntimeID
	}
	res.PolicyModel = rule.Model
	res.PolicyThinkingLevel = rule.ThinkingLevel
	res.PolicyServiceTier = rule.ServiceTier
	return res
}

// policyRoleCodeText wraps a resolved role_code into the audit column value
// stamped on task rows. It stays NULL (invalid) for agent_default routing —
// only a matched rule (bind_agent / exec_override) writes evidence of WHICH
// policy rule overrode the routing.
func policyRoleCodeText(roleCode string) pgtype.Text {
	if roleCode == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: roleCode, Valid: true}
}

// applyRolePolicy resolves the workspace role policy for a task about to be
// enqueued for agent and returns the values to stamp on the new task row:
// the resolved executor agent id, resolved runtime id, and the exec overrides
// (policy_model / policy_thinking_level / policy_service_tier). It returns
// ErrRolePolicyDisabled when the role is disabled by policy (fail-closed), in
// which case the caller must NOT create the task.
func (s *TaskService) applyRolePolicy(ctx context.Context, workspaceID pgtype.UUID, agent db.Agent) (taskAgentID, taskRuntimeID pgtype.UUID, policyModel, policyThinkingLevel, policyServiceTier pgtype.Text, roleCode string, err error) {
	res := s.ResolveRolePolicy(ctx, workspaceID, agent)
	switch res.Mode {
	case RolePolicyDisabled:
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, res.RoleCode, ErrRolePolicyDisabled
	case RolePolicyBindAgent:
		slog.Info("task enqueue: role policy bind_agent",
			"role_code", res.RoleCode,
			"assigned_agent_id", util.UUIDToString(agent.ID),
			"resolved_agent_id", util.UUIDToString(res.AgentID),
			"resolved_runtime_id", util.UUIDToString(res.RuntimeID),
		)
		return res.AgentID, res.RuntimeID, pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, res.RoleCode, nil
	case RolePolicyExecOverride:
		slog.Info("task enqueue: role policy exec_override",
			"role_code", res.RoleCode,
			"agent_id", util.UUIDToString(res.AgentID),
			"resolved_runtime_id", util.UUIDToString(res.RuntimeID),
			"resolved_model", res.PolicyModel.String,
		)
		return res.AgentID, res.RuntimeID, res.PolicyModel, res.PolicyThinkingLevel, res.PolicyServiceTier, res.RoleCode, nil
	default:
		return agent.ID, agent.RuntimeID, pgtype.Text{}, pgtype.Text{}, pgtype.Text{}, "", nil
	}
}
