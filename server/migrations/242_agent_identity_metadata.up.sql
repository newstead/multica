ALTER TABLE agent
    ADD COLUMN IF NOT EXISTS role_code TEXT,
    ADD COLUMN IF NOT EXISTS language_codes TEXT[];

ALTER TABLE agent
    DROP CONSTRAINT IF EXISTS agent_language_codes_check,
    DROP CONSTRAINT IF EXISTS agent_role_code_check;

ALTER TABLE agent
    ADD CONSTRAINT agent_role_code_check CHECK (
        role_code IS NULL OR role_code IN ('TL', 'BE', 'FE', 'FS', 'QA', 'OPS', 'ML', 'DA', 'SRE', 'SEC')
    ),
    ADD CONSTRAINT agent_language_codes_check CHECK (
        language_codes IS NULL OR language_codes <@ ARRAY['GO', 'PY', 'TS', 'JS', 'RS', 'SH', 'RB', 'JV', 'KT', 'SW', 'CS', 'CP', 'SC', 'EL']::TEXT[]
    );
