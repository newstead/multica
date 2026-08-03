-- Workspace role → AI execution config policy (ROLEPOL-0 / ROL-214).

-- name: GetWorkspaceRolePolicyEnabled :one
-- Lean read of the role-policy toggle for the enqueue hot path, avoiding a
-- full workspace-row fetch (mirrors GetWorkspaceAttributionFailClosed).
SELECT role_policy_enabled FROM workspace WHERE id = $1;

-- name: ListWorkspaceRolePolicies :many
-- Full policy matrix for a workspace (at most 10 rows, one per role_code).
SELECT * FROM workspace_role_policy
WHERE workspace_id = $1
ORDER BY role_code ASC;

-- name: SetWorkspaceRolePolicyEnabled :exec
-- Flips the workspace-level toggle. Rules are replaced separately by
-- DeleteWorkspaceRolePolicies + InsertWorkspaceRolePolicy inside the same
-- transaction so PUT is an atomic full replacement.
UPDATE workspace SET role_policy_enabled = $2, updated_at = now() WHERE id = $1;

-- name: DeleteWorkspaceRolePolicies :exec
DELETE FROM workspace_role_policy WHERE workspace_id = $1;

-- name: InsertWorkspaceRolePolicy :exec
INSERT INTO workspace_role_policy (
    workspace_id, role_code, agent_id, runtime_id, model,
    thinking_level, service_tier, fallback, updated_by
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
);
