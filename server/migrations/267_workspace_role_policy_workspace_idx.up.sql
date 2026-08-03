-- Workspace role policy is read per-workspace on the enqueue hot path
-- (ListWorkspaceRolePolicies), so the workspace_id lookup needs an index.
-- Kept in its own single-statement file per CLAUDE.md: every index created
-- by a migration must use CREATE INDEX CONCURRENTLY, and PostgreSQL rejects
-- concurrent index builds inside a transaction / multi-command string.
CREATE INDEX CONCURRENTLY IF NOT EXISTS workspace_role_policy_workspace_idx
    ON workspace_role_policy (workspace_id);
