-- Revert to the original ru/en-only contract (migrations 236-238).
ALTER TABLE workspace
    DROP CONSTRAINT workspace_language_policy_check,
    ADD CONSTRAINT workspace_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );

ALTER TABLE project
    DROP CONSTRAINT project_language_policy_check,
    ADD CONSTRAINT project_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );

ALTER TABLE agent
    DROP CONSTRAINT agent_language_policy_check,
    ADD CONSTRAINT agent_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );
