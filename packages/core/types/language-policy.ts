/**
 * Language policy for agent-produced text: issue comments, handoff notes,
 * blocker reports, autopilot summaries, and agent-created issue
 * titles/descriptions.
 *
 * The value is a BCP-47 tag stored on `workspace`, `project`, and `agent`
 * (`language_policy TEXT NULL`). Resolution happens server-side at claim
 * time: `agent > project > workspace`. Unset on every level keeps the
 * current behavior (no policy, no runtime-brief section).
 *
 * This is intentionally separate from:
 *   - `user.language` — UI locale only, never a runtime policy;
 *   - `agent.language_codes` — programming languages for identity badges.
 *
 * The UI and server validation restrict values to this supported list;
 * invalid/empty codes are treated as unset and inherit up the chain.
 */
export const AGENT_LANGUAGE_POLICY_VALUES = [
  "ru",
  "en",
  "zh-Hans",
  "ja",
  "ko",
] as const;

export type AgentLanguagePolicy = (typeof AGENT_LANGUAGE_POLICY_VALUES)[number];

export function isAgentLanguagePolicy(
  value: string | null | undefined,
): value is AgentLanguagePolicy {
  return (
    !!value &&
    (AGENT_LANGUAGE_POLICY_VALUES as readonly string[]).includes(value)
  );
}
