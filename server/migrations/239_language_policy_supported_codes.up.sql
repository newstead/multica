-- Align the language_policy CHECK constraints with the canonical supported
-- list in packages/core/types/language-policy.ts (AGENT_LANGUAGE_POLICY_VALUES):
-- the UI offers zh-Hans/ja/ko in addition to ru/en, so the backend must accept
-- the same codes or a UI selection would 400.
ALTER TABLE workspace
    DROP CONSTRAINT workspace_language_policy_check,
    ADD CONSTRAINT workspace_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en', 'zh-Hans', 'ja', 'ko')
    );

ALTER TABLE project
    DROP CONSTRAINT project_language_policy_check,
    ADD CONSTRAINT project_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en', 'zh-Hans', 'ja', 'ko')
    );

ALTER TABLE agent
    DROP CONSTRAINT agent_language_policy_check,
    ADD CONSTRAINT agent_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en', 'zh-Hans', 'ja', 'ko')
    );
