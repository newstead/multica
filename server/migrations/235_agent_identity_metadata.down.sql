ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_language_codes_check,
    DROP CONSTRAINT IF EXISTS agent_role_code_check,
    DROP COLUMN IF EXISTS language_codes,
    DROP COLUMN IF EXISTS role_code;
