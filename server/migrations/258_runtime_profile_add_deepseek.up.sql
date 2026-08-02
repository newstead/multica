-- Add DeepSeek (`deepseek`) to the built-in runtime profile protocol whitelist.
-- Also keeps Qoder CN (`qoderclicn`) from upstream migration 242: the
-- constraint is dropped and re-created here as the union (deepseek +
-- qoderclicn + all prior families) so the final whitelist matches
-- agent.SupportedTypes after the upstream v0.4.16 merge. Kept in lockstep with
-- agent.SupportedTypes and agent.New(). NOT VALID preserves the historical-row
-- tolerance used by the prior family additions.
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
