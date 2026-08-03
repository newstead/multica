export type MemberRole = "owner" | "admin" | "member";

export interface WorkspaceRepo {
  url: string;
  description?: string;
}

export interface Workspace {
  id: string;
  name: string;
  slug: string;
  description: string | null;
  context: string | null;
  settings: Record<string, unknown>;
  /**
   * Default language policy for agent task output (BCP-47, e.g. "ru").
   * `null` means unset — agents keep the current behavior (no policy).
   * Projects and agents can override this. See
   * `packages/core/types/language-policy.ts`. Optional so workspaces from
   * older backends still parse; treat missing as unset.
   */
  language_policy?: string | null;
  repos: WorkspaceRepo[];
  issue_prefix: string;
  avatar_url: string | null;
  created_at: string;
  updated_at: string;
}

export interface Member {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
}

export interface User {
  id: string;
  name: string;
  email: string;
  avatar_url: string | null;
  onboarded_at: string | null;
  /**
   * JSONB payload from the server. Typed as `unknown` here so this module
   * stays independent of the questionnaire shape — the onboarding views
   * cast into `Partial<QuestionnaireAnswers>` when reading. Server always
   * returns an object (defaults to `{}`), never null.
   */
  onboarding_questionnaire: Record<string, unknown>;
  /**
   * Legacy column from the removed starter-content dialog. The column is
   * still written to (always 'imported' for new accounts after the
   * mark-onboarded paths run) so older desktop builds — which still render
   * the dialog on NULL — don't show it to anyone created on a newer server.
   * Kept as `string | null` for forward compatibility.
   */
  starter_content_state: string | null;
  /** Preferred UI language. null means "follow client/system". */
  language: string | null;
  /**
   * Free-form self-description (role, stack, preferences). Injected into
   * the agent brief so coding agents have cheap, durable context about
   * who is requesting the work. Server always returns a string —
   * NOT NULL DEFAULT '' at the column level, empty when unset.
   */
  profile_description: string;
  /** Pinned IANA tz; null means "use browser-detected tz at render time". */
  timezone: string | null;
  created_at: string;
  updated_at: string;
}

export interface MemberWithUser {
  id: string;
  workspace_id: string;
  user_id: string;
  role: MemberRole;
  created_at: string;
  name: string;
  email: string;
  avatar_url: string | null;
}

export interface Invitation {
  id: string;
  workspace_id: string;
  inviter_id: string;
  invitee_email: string;
  invitee_user_id: string | null;
  role: MemberRole;
  status: "pending" | "accepted" | "declined" | "expired";
  created_at: string;
  updated_at: string;
  expires_at: string;
  inviter_name?: string;
  inviter_email?: string;
  workspace_name?: string;
}

/**
 * Workspace role → AI execution config policy (ROLEPOL-0 / ROL-214).
 *
 * One matrix row per canonical agent role (`role_code`). A row either binds
 * the role to a concrete agent (Variant A, XOR with exec fields) or layers
 * exec overrides on top of the assigned agent (Variant B). `fallback`
 * decides what happens when the policy cannot be applied for the role:
 * `agent_default` keeps the historical agent-centric behavior, `disabled`
 * fails closed (no task is created).
 */
export type RolePolicyFallback = "agent_default" | "disabled";

export interface WorkspaceRolePolicyRule {
  /** Variant A: run tasks for this role as this agent. XOR with exec fields. */
  agent_id?: string | null;
  /** Variant B: exec config layered over the assigned agent. */
  runtime_id?: string | null;
  model?: string | null;
  thinking_level?: string | null;
  service_tier?: string | null;
  fallback: RolePolicyFallback;
}

export interface WorkspaceRolePolicy {
  enabled: boolean;
  /** Keyed by canonical role_code (`TL`, `BE`, ...). */
  rules: Record<string, WorkspaceRolePolicyRule>;
}
