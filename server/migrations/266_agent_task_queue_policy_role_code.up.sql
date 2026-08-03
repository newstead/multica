-- Role policy audit evidence (ROLEPOL-3 / ROL-211).
--
-- Records WHICH role rule was applied to a task at enqueue time (the canonical
-- role_code, e.g. 'QA') when workspace role policy overrode the routing
-- (bind_agent or exec_override). NULL means the task ran under agent_default
-- (feature off, no role_code, no matching rule, or degradation) — exactly the
-- legacy agent-centric behavior, unchanged. Combined with the existing
-- policy_model/policy_thinking_level/policy_service_tier columns this makes
-- every policy-routed task self-describing for audit: bind_agent shows the
-- role and the resolved agent_id, exec_override shows the role plus the exec
-- overrides. Free TEXT with no CHECK so future role vocabulary additions do
-- not require a migration.

ALTER TABLE agent_task_queue
    ADD COLUMN policy_role_code text NULL;
