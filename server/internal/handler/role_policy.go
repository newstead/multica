package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Workspace role → AI execution config policy (ROLEPOL-0 / ROL-214).
//
// GET  /api/workspaces/{workspaceId}/role-policy — member-visible.
// PUT  /api/workspaces/{workspaceId}/role-policy — owner/admin, full
// replacement of the matrix + enabled toggle (same middleware as
// UpdateWorkspace).

// canonicalRoleCodes is the canonical role space; the policy matrix is keyed
// by these role_code values (mirrors validAgentRoleCodes in agent.go).
var canonicalRoleCodes = []string{"TL", "BE", "FE", "FS", "QA", "OPS", "ML", "DA", "SRE", "SEC"}

// rolePolicyRule is the wire shape of one matrix row. Agent binding (Variant A)
// is XOR with the exec fields (Variant B); fallback is 'agent_default' (no rule
// fallback) or 'disabled' (fail-closed refusal).
type rolePolicyRule struct {
	AgentID       *string `json:"agent_id,omitempty"`
	RuntimeID     *string `json:"runtime_id,omitempty"`
	Model         *string `json:"model,omitempty"`
	ThinkingLevel *string `json:"thinking_level,omitempty"`
	ServiceTier   *string `json:"service_tier,omitempty"`
	Fallback      string  `json:"fallback"`
}

// rolePolicyResponse is the GET/PUT response shape.
type rolePolicyResponse struct {
	Enabled bool                      `json:"enabled"`
	Rules   map[string]rolePolicyRule `json:"rules"`
}

// updateRolePolicyRequest is the PUT body: full replacement of the matrix plus
// the enabled toggle. Rules keys are role_codes, normalized to uppercase.
type updateRolePolicyRequest struct {
	Enabled bool                        `json:"enabled"`
	Rules   map[string]rolePolicyRuleIn `json:"rules"`
}

// rolePolicyRuleIn is the PUT wire shape of one matrix row.
type rolePolicyRuleIn struct {
	AgentID       *string `json:"agent_id"`
	RuntimeID     *string `json:"runtime_id"`
	Model         *string `json:"model"`
	ThinkingLevel *string `json:"thinking_level"`
	ServiceTier   *string `json:"service_tier"`
	Fallback      string  `json:"fallback"`
}

// GetWorkspaceRolePolicy returns the workspace's role policy matrix.
// Access: any workspace member (router middleware).
func (h *Handler) GetWorkspaceRolePolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	if _, ok := h.requireWorkspaceMember(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	ws, err := h.Queries.GetWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	rules, err := h.Queries.ListWorkspaceRolePolicies(r.Context(), workspaceUUID)
	if err != nil {
		slog.Warn("role policy get failed", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to load role policy")
		return
	}
	writeJSON(w, http.StatusOK, rolePolicyToResponse(ws.RolePolicyEnabled, rules))
}

// UpdateWorkspaceRolePolicy replaces the workspace role policy matrix and the
// enabled toggle atomically. Access: owner/admin (enforced by the router
// middleware RequireWorkspaceRoleFromURL("id","owner","admin"), same as
// UpdateWorkspace).
func (h *Handler) UpdateWorkspaceRolePolicy(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	var req updateRolePolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	normalized, err := h.validateRolePolicyRules(r.Context(), workspaceUUID, req.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	userUUID, ok := parseUUIDOrBadRequest(w, requestUserID(r), "user id")
	if !ok {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		slog.Error("role policy update: begin tx failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to update role policy")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)

	if err := qtx.SetWorkspaceRolePolicyEnabled(r.Context(), db.SetWorkspaceRolePolicyEnabledParams{
		ID:                workspaceUUID,
		RolePolicyEnabled: req.Enabled,
	}); err != nil {
		slog.Warn("role policy update: set enabled failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to update role policy")
		return
	}
	if err := qtx.DeleteWorkspaceRolePolicies(r.Context(), workspaceUUID); err != nil {
		slog.Warn("role policy update: delete rules failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to update role policy")
		return
	}
	for roleCode, rule := range normalized {
		params := db.InsertWorkspaceRolePolicyParams{
			WorkspaceID: workspaceUUID,
			RoleCode:    roleCode,
			Fallback:    rule.Fallback,
			UpdatedBy:   userUUID,
		}
		if rule.AgentID.Valid {
			params.AgentID = rule.AgentID
		}
		if rule.RuntimeID.Valid {
			params.RuntimeID = rule.RuntimeID
		}
		params.Model = rule.Model
		params.ThinkingLevel = rule.ThinkingLevel
		params.ServiceTier = rule.ServiceTier
		if err := qtx.InsertWorkspaceRolePolicy(r.Context(), params); err != nil {
			slog.Warn("role policy update: insert rule failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID, "role_code", roleCode)...)
			writeError(w, http.StatusInternalServerError, "failed to update role policy")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		slog.Error("role policy update: commit failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to update role policy")
		return
	}

	// Re-read the committed matrix so the response is authoritative.
	ws, err := h.Queries.GetWorkspace(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload role policy")
		return
	}
	rules, err := h.Queries.ListWorkspaceRolePolicies(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reload role policy")
		return
	}
	slog.Info("workspace role policy updated", append(logger.RequestAttrs(r), "workspace_id", workspaceID, "enabled", req.Enabled, "rule_count", len(normalized))...)
	writeJSON(w, http.StatusOK, rolePolicyToResponse(ws.RolePolicyEnabled, rules))
}

// validateRolePolicyRules normalizes and validates a PUT matrix. Each key must
// be a canonical role_code (uppercase, no spaces); each rule must satisfy the
// XOR agent-vs-exec contract and reference agents/runtimes that exist in the
// workspace, are not archived, and (for exec fields) are advertised by the
// runtime's model catalog when one is cached.
func (h *Handler) validateRolePolicyRules(ctx context.Context, workspaceID pgtype.UUID, rules map[string]rolePolicyRuleIn) (map[string]db.WorkspaceRolePolicy, error) {
	out := make(map[string]db.WorkspaceRolePolicy, len(rules))
	canonical := make(map[string]struct{}, len(canonicalRoleCodes))
	for _, code := range canonicalRoleCodes {
		canonical[code] = struct{}{}
	}

	for rawCode, rule := range rules {
		roleCode := strings.ToUpper(strings.TrimSpace(rawCode))
		if _, ok := canonical[roleCode]; !ok {
			return nil, fmt.Errorf("role %q: role_code must be one of %s", rawCode, strings.Join(canonicalRoleCodes, ", "))
		}
		if rule.Fallback == "" {
			rule.Fallback = "agent_default"
		}
		if rule.Fallback != "agent_default" && rule.Fallback != "disabled" {
			return nil, fmt.Errorf("role %s: fallback must be 'agent_default' or 'disabled'", roleCode)
		}

		row := db.WorkspaceRolePolicy{
			WorkspaceID: workspaceID,
			RoleCode:    roleCode,
			Fallback:    rule.Fallback,
		}

		var agentID pgtype.UUID
		var runtimeID pgtype.UUID
		hasExec := rule.RuntimeID != nil || rule.Model != nil || rule.ThinkingLevel != nil || rule.ServiceTier != nil

		if rule.AgentID != nil && strings.TrimSpace(*rule.AgentID) != "" {
			agentUUID, err := util.ParseUUID(strings.TrimSpace(*rule.AgentID))
			if err != nil {
				return nil, fmt.Errorf("role %s: agent_id is not a valid UUID", roleCode)
			}
			if hasExec {
				return nil, fmt.Errorf("role %s: agent_id is mutually exclusive with runtime_id/model/thinking_level/service_tier", roleCode)
			}
			agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
				ID:          agentUUID,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return nil, fmt.Errorf("role %s: agent_id %s is not an active agent in this workspace", roleCode, *rule.AgentID)
			}
			if agent.ArchivedAt.Valid {
				return nil, fmt.Errorf("role %s: agent_id %s is archived", roleCode, *rule.AgentID)
			}
			if !agent.RuntimeID.Valid {
				return nil, fmt.Errorf("role %s: agent_id %s has no runtime", roleCode, *rule.AgentID)
			}
			agentID = agent.ID
		}

		if rule.RuntimeID != nil && strings.TrimSpace(*rule.RuntimeID) != "" {
			runtimeUUID, err := util.ParseUUID(strings.TrimSpace(*rule.RuntimeID))
			if err != nil {
				return nil, fmt.Errorf("role %s: runtime_id is not a valid UUID", roleCode)
			}
			runtime, err := h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
				ID:          runtimeUUID,
				WorkspaceID: workspaceID,
			})
			if err != nil {
				return nil, fmt.Errorf("role %s: runtime_id %s is not a runtime in this workspace", roleCode, *rule.RuntimeID)
			}
			runtimeID = runtime.ID
			if err := h.validateRolePolicyExecFields(ctx, runtime, rule, roleCode); err != nil {
				return nil, err
			}
		} else if rule.Model != nil || rule.ThinkingLevel != nil || rule.ServiceTier != nil {
			return nil, fmt.Errorf("role %s: model/thinking_level/service_tier require a runtime_id", roleCode)
		}

		row.AgentID = agentID
		row.RuntimeID = runtimeID
		row.Model = ptrToText(rule.Model)
		row.ThinkingLevel = ptrToText(rule.ThinkingLevel)
		row.ServiceTier = ptrToText(rule.ServiceTier)
		out[roleCode] = row
	}
	return out, nil
}

// validateRolePolicyExecFields checks model/thinking_level/service_tier against
// the runtime's cached model catalog when one is available (the server cannot
// reach the daemon's local CLI catalog directly). A missing catalog is not a
// validation failure — the daemon's ValidateServiceTier / ValidateThinkingLevel
// guards remain the safety net, matching the claim path.
func (h *Handler) validateRolePolicyExecFields(ctx context.Context, runtime db.AgentRuntime, rule rolePolicyRuleIn, roleCode string) error {
	snapshot := h.cachedModelCatalog(ctx, uuidToString(runtime.ID))
	if snapshot == nil {
		slog.Warn("role policy: model catalog unavailable; skipping catalog validation",
			"runtime_id", uuidToString(runtime.ID),
			"role_code", roleCode,
		)
		return nil
	}

	model := ""
	if rule.Model != nil {
		model = strings.TrimSpace(*rule.Model)
	}
	var entry *ModelEntry
	for i := range snapshot.Models {
		if snapshot.Models[i].ID == model {
			entry = &snapshot.Models[i]
			break
		}
	}

	if rule.ThinkingLevel != nil && strings.TrimSpace(*rule.ThinkingLevel) != "" {
		if entry == nil || entry.Thinking == nil {
			return fmt.Errorf("role %s: thinking_level %q is not valid for model %q on this runtime", roleCode, *rule.ThinkingLevel, model)
		}
		valid := false
		for _, lvl := range entry.Thinking.SupportedLevels {
			if lvl.Value == strings.TrimSpace(*rule.ThinkingLevel) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("role %s: thinking_level %q is not valid for model %q on this runtime", roleCode, *rule.ThinkingLevel, model)
		}
	}

	if rule.ServiceTier != nil && strings.TrimSpace(*rule.ServiceTier) != "" {
		if entry == nil {
			return fmt.Errorf("role %s: service_tier %q is not valid for model %q on this runtime", roleCode, *rule.ServiceTier, model)
		}
		valid := false
		for _, tier := range entry.ServiceTiers {
			if tier.ID == strings.TrimSpace(*rule.ServiceTier) {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("role %s: service_tier %q is not valid for model %q on this runtime", roleCode, *rule.ServiceTier, model)
		}
	}
	return nil
}

// rolePolicyToResponse maps the DB rows onto the wire shape.
func rolePolicyToResponse(enabled bool, rules []db.WorkspaceRolePolicy) rolePolicyResponse {
	out := rolePolicyResponse{
		Enabled: enabled,
		Rules:   make(map[string]rolePolicyRule, len(rules)),
	}
	for _, rule := range rules {
		row := rolePolicyRule{Fallback: rule.Fallback}
		if rule.AgentID.Valid {
			row.AgentID = stringPtr(uuidToString(rule.AgentID))
		}
		if rule.RuntimeID.Valid {
			row.RuntimeID = stringPtr(uuidToString(rule.RuntimeID))
		}
		row.Model = textToPtr(rule.Model)
		row.ThinkingLevel = textToPtr(rule.ThinkingLevel)
		row.ServiceTier = textToPtr(rule.ServiceTier)
		out.Rules[rule.RoleCode] = row
	}
	return out
}

// stringPtr returns a pointer to s (JSON omitempty-friendly helper for
// optional wire fields).
func stringPtr(s string) *string { return &s }
