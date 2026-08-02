ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS language_policy TEXT;

ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_language_policy_check;

ALTER TABLE agent
    ADD CONSTRAINT agent_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );
