-- Add DeepSeek (`deepseek`) to the built-in runtime profile protocol whitelist.
-- Runs after upstream 242 (qoderclicn) during the v0.4.16 sync, so it keeps
-- qoderclicn in the whitelist as well — the final constraint must contain both
-- fork and upstream family additions. Kept in lockstep with
-- agent.SupportedTypes and agent.New().  NOT VALID preserves the
-- historical-row tolerance used by the prior family additions.
ALTER TABLE runtime_profile DROP CONSTRAINT IF EXISTS runtime_profile_protocol_family_check;

ALTER TABLE runtime_profile ADD CONSTRAINT runtime_profile_protocol_family_check
    CHECK (protocol_family IN (
        'claude',
        'codebuddy',
        'codex',
        'deepseek',
        'copilot',
        'opencode',
        'openclaw',
        'hermes',
        'pi',
        'cursor',
        'kimi',
        'kiro',
        'antigravity',
        'qoder',
        'qoderclicn',
        'traecli',
        'deveco',
        'grok',
        'qwen'
    )) NOT VALID;
