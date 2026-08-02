ALTER TABLE project
    ADD COLUMN IF NOT EXISTS language_policy TEXT;

ALTER TABLE project
    DROP CONSTRAINT IF EXISTS project_language_policy_check;

ALTER TABLE project
    ADD CONSTRAINT project_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );
