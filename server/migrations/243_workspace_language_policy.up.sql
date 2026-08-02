ALTER TABLE workspace
    ADD COLUMN language_policy TEXT,
    ADD CONSTRAINT workspace_language_policy_check CHECK (
        language_policy IS NULL OR language_policy IN ('ru', 'en')
    );
