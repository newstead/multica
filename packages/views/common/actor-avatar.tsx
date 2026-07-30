"use client";

import { useEffect, useId, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { ActorAvatar as ActorAvatarBase } from "@multica/ui/components/common/actor-avatar";
import { AVATAR_SIZE_PX, type AvatarSize } from "@multica/ui/lib/avatar-size";
import { parseAvatarEmoji } from "@multica/ui/lib/avatar-emoji";
import { cn } from "@multica/ui/lib/utils";
import {
  HoverCard,
  HoverCardTrigger,
  HoverCardContent,
} from "@multica/ui/components/ui/hover-card";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { useActorName } from "@multica/core/workspace/hooks";
import { useAgentPresenceDetail } from "@multica/core/agents";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { AgentProfileCard } from "../agents/components/agent-profile-card";
import { AgentLivePeekCard } from "../agents/components/agent-live-peek-card";
import { MemberProfileCard } from "../members/member-profile-card";
import { SquadProfileCard } from "../squads/components/squad-profile-card";
import { availabilityConfig } from "../agents/presence";
import { useT } from "../i18n";
import { useNavigation } from "../navigation";

/**
 * Selects which agent hover-card payload to render when `enableHoverCard` is
 * on. Two surfaces, two intents:
 * - `"profile"` (default) — static identity (description, runtime, skills,
 *   owner). Used by 20+ "who is this agent?" surfaces (comment authors,
 *   pickers, list rows).
 * - `"live"` — live activity peek (workload, current issue, last activity).
 *   Used where the user already knows the identity and wants the live state,
 *   e.g. the squad members tab.
 *
 * Has no effect for non-agent actors (members always render the member card).
 */
export type AgentHoverCardVariant = "profile" | "live";
export type AgentIdentityBadgeVariant = "corner-tag" | "inline-row" | "chip-below";
export type AgentIdentityBadgeHostMode = "owned" | "standalone";

type AgentIdentity = {
  roleCode: string | null;
  languageCodes: string[];
};

type AgentIdentityBadgeModel = {
  text: string;
  roleCode: string | null;
  languageCodes: string[];
};

interface ActorAvatarProps {
  actorType: string;
  actorId: string;
  size?: AvatarSize;
  className?: string;
  /**
   * Wrap the avatar in a hover-card preview on dwell. Use for "who is this?"
   * surfaces — comment authors, list rows, subscriber chips. Independent of
   * `showStatusDot`: a surface can have one, both, or neither.
   */
  enableHoverCard?: boolean;
  /**
   * Overlay an agent-presence dot at the avatar's bottom-right. Use at
   * decision moments (picker rows, current-assignee display, agent-centric
   * surfaces). Has no effect for non-agent actors. Independent of
   * `enableHoverCard` so picker rows can show the dot without nesting a
   * popover inside the dropdown.
   */
  showStatusDot?: boolean;
  /**
   * When `enableHoverCard` is on for an agent, choose which payload to
   * render. See {@link AgentHoverCardVariant}. Defaults to `"profile"` so
   * existing call sites keep their identity-card behaviour.
   */
  hoverCardVariant?: AgentHoverCardVariant;
  /**
   * Opt an agent surface into the standardized ROLE x LANGUAGE badge. The
   * default host mode is "owned" because most avatar call sites live inside a
   * row, menu item, link, command item, or button that already owns focus.
   */
  identityBadge?:
    | boolean
    | {
        variant?: AgentIdentityBadgeVariant;
        hostMode?: AgentIdentityBadgeHostMode;
      };
  /**
   * Make the avatar click through to the actor page. Defaults on for members
   * and agents, while picker/menu controls keep their own click behavior.
   */
  profileLink?: boolean;
}

const FOCUSABLE_ANCESTOR_SELECTOR =
  'a[href], button:not([disabled]), [role="button"]:not([aria-disabled="true"]), [tabindex]:not([tabindex="-1"])';
const PROFILE_LINK_CONTROL_SELECTOR =
  'button, [role^="menuitem"], [role="option"], [data-slot="dropdown-menu-item"], [data-slot="dropdown-menu-checkbox-item"], [data-slot="popover-trigger"]';

const ROLE_CODES = ["TL", "BE", "FE", "FS", "QA", "OPS", "ML", "DA", "SRE", "SEC"] as const;
const LANGUAGE_CODES = ["GO", "PY", "TS", "JS", "RS", "SH", "RB", "JV", "KT", "SW", "CS", "CP", "SC", "EL"] as const;
type KnownRoleCode = (typeof ROLE_CODES)[number];
type KnownLanguageCode = (typeof LANGUAGE_CODES)[number];

const ROLE_CLASS: Record<KnownRoleCode, string> = {
  TL: "border-emerald-300 bg-emerald-50 text-emerald-950 dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-100",
  BE: "border-sky-300 bg-sky-50 text-sky-950 dark:border-sky-700 dark:bg-sky-950 dark:text-sky-100",
  FE: "border-violet-300 bg-violet-50 text-violet-950 dark:border-violet-700 dark:bg-violet-950 dark:text-violet-100",
  FS: "border-indigo-300 bg-indigo-50 text-indigo-950 dark:border-indigo-700 dark:bg-indigo-950 dark:text-indigo-100",
  QA: "border-amber-300 bg-amber-50 text-amber-950 dark:border-amber-700 dark:bg-amber-950 dark:text-amber-100",
  OPS: "border-red-300 bg-red-50 text-red-950 dark:border-red-700 dark:bg-red-950 dark:text-red-100",
  ML: "border-teal-300 bg-teal-50 text-teal-950 dark:border-teal-700 dark:bg-teal-950 dark:text-teal-100",
  DA: "border-cyan-300 bg-cyan-50 text-cyan-950 dark:border-cyan-700 dark:bg-cyan-950 dark:text-cyan-100",
  SRE: "border-orange-300 bg-orange-50 text-orange-950 dark:border-orange-700 dark:bg-orange-950 dark:text-orange-100",
  SEC: "border-rose-300 bg-rose-50 text-rose-950 dark:border-rose-700 dark:bg-rose-950 dark:text-rose-100",
};

function normalizeIdentityCode(value: string | null | undefined) {
  const normalized = value?.trim().toUpperCase();
  if (!normalized) return null;
  return normalized.replace(/\s+/g, "");
}

function normalizeLanguageCodes(values: readonly string[] | null | undefined) {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values ?? []) {
    const normalized = normalizeIdentityCode(value);
    if (!normalized || seen.has(normalized)) continue;
    seen.add(normalized);
    result.push(normalized);
  }
  return result;
}

export function buildAgentIdentityBadgeModel(
  identity: AgentIdentity | null,
  size: AvatarSize | undefined,
  variant: AgentIdentityBadgeVariant,
): AgentIdentityBadgeModel | null {
  if (!identity || size === "xs") return null;

  const roleCode = normalizeIdentityCode(identity.roleCode);
  const languageCodes = normalizeLanguageCodes(identity.languageCodes);
  if (!roleCode && languageCodes.length === 0) return null;

  const languageText =
    languageCodes.length > 1
      ? `+${languageCodes.length}`
      : languageCodes[0]
        ? displayIdentityCode(languageCodes[0], "language")
        : null;
  const showLanguage = Boolean(
    languageText && !(variant === "corner-tag" && size === "sm"),
  );
  const roleText = roleCode ? displayIdentityCode(roleCode, "role") : null;
  const text = roleCode
    ? showLanguage
      ? `${roleText}\u00b7${languageText}`
      : roleText
    : languageText;

  return text
    ? {
        text,
        roleCode,
        languageCodes,
      }
    : null;
}

export function ActorAvatar({
  actorType,
  actorId,
  size,
  className,
  enableHoverCard,
  showStatusDot,
  hoverCardVariant = "profile",
  identityBadge,
  profileLink,
}: ActorAvatarProps) {
  const actorResolver = useActorName();
  const { getActorName, getActorInitials, getActorAvatarUrl } = actorResolver;
  const getAgentIdentity = "getAgentIdentity" in actorResolver
    ? actorResolver.getAgentIdentity
    : () => null;
  const { t } = useT("agents");
  const paths = useWorkspacePaths();
  const name = getActorName(actorType, actorId);
  const avatarUrl = getActorAvatarUrl(actorType, actorId);
  const badgeOptions =
    identityBadge === true
      ? {}
      : identityBadge && typeof identityBadge === "object"
        ? identityBadge
        : null;
  const badgeVariant = badgeOptions?.variant ?? "corner-tag";
  const badgeHostMode = badgeOptions?.hostMode ?? "owned";
  const badgeModel = badgeOptions
    ? buildAgentIdentityBadgeModel(
        getAgentIdentity(actorType, actorId),
        size,
        badgeVariant,
      )
    : null;
  const badgeLabel = badgeModel ? formatAgentIdentityBadgeLabel(t, badgeModel) : null;
  const standardizedAgentAvatarUrl =
    badgeModel && parseAvatarEmoji(avatarUrl) ? null : avatarUrl;
  const avatar = (
    <ActorAvatarBase
      name={name}
      ariaLabel={badgeLabel ? `${name}: ${badgeLabel}` : undefined}
      initials={getActorInitials(actorType, actorId)}
      avatarUrl={standardizedAgentAvatarUrl}
      isAgent={actorType === "agent"}
      isSystem={actorType === "system"}
      isSquad={actorType === "squad"}
      size={size}
      className={className}
    />
  );

  // Optional presence dot overlay. Only meaningful for agents — members have
  // no presence backbone. Wrapping unconditionally with relative inline-flex
  // would create extra DOM for every avatar; we only wrap when a dot is asked
  // for.
  const wrapDot = showStatusDot && actorType === "agent";
  const dotted = wrapDot ? (
    <span className="relative inline-flex">
      {avatar}
      <AgentStatusDot
        agentId={actorId}
        size={size}
        placement={badgeModel ? "top" : "bottom"}
      />
    </span>
  ) : (
    avatar
  );
  const shouldLinkToProfile =
    profileLink ??
    (actorType === "member" || actorType === "agent" || actorType === "squad");
  const profileHref = shouldLinkToProfile
    ? actorType === "member"
      ? paths.memberDetail(actorId)
      : actorType === "agent"
        ? paths.agentDetail(actorId)
        : actorType === "squad"
          ? paths.squadDetail(actorId)
          : null
    : null;
  const avatarContent = profileHref ? (
    <ActorAvatarProfileLink href={profileHref}>{dotted}</ActorAvatarProfileLink>
  ) : (
    dotted
  );

  let content = avatarContent;
  if (enableHoverCard && actorType === "agent") {
    content = (
      <AgentAvatarHoverCard agentId={actorId} variant={hoverCardVariant}>
        {avatarContent}
      </AgentAvatarHoverCard>
    );
  }
  if (enableHoverCard && actorType === "member") {
    content = <MemberAvatarHoverCard userId={actorId}>{avatarContent}</MemberAvatarHoverCard>;
  }
  if (enableHoverCard && actorType === "squad") {
    content = <SquadAvatarHoverCard squadId={actorId}>{avatarContent}</SquadAvatarHoverCard>;
  }

  return badgeModel && badgeLabel ? (
    <AgentIdentityAvatarFrame
      badge={badgeModel}
      label={badgeLabel}
      variant={badgeVariant}
      hostMode={badgeHostMode}
    >
      {content}
    </AgentIdentityAvatarFrame>
  ) : content;
}

function ActorAvatarProfileLink({
  href,
  children,
}: {
  href: string;
  children: React.ReactNode;
}) {
  const { push, openInNewTab } = useNavigation();

  const navigate = (event: React.MouseEvent | React.KeyboardEvent) => {
    const controlAncestor = event.currentTarget.parentElement?.closest(
      PROFILE_LINK_CONTROL_SELECTOR,
    );
    if (controlAncestor) return;

    event.preventDefault();
    event.stopPropagation();
    if (
      "metaKey" in event &&
      (event.metaKey || event.ctrlKey || event.shiftKey) &&
      openInNewTab
    ) {
      openInNewTab(href);
      return;
    }
    push(href);
  };

  return (
    <span
      role="link"
      tabIndex={-1}
      className="inline-flex cursor-pointer rounded-full"
      onClick={navigate}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          navigate(event);
        }
      }}
    >
      {children}
    </span>
  );
}

// Small presence indicator overlaid on an agent avatar. The dot scales with the
// avatar size — anything >=24 px gets the standard 8 px dot, smaller avatars use
// a 6 px dot so the indicator doesn't overwhelm them.
export function AgentStatusDot({
  agentId,
  size,
  placement = "bottom",
}: {
  agentId: string;
  size?: AvatarSize;
  placement?: "bottom" | "top";
}) {
  const ws = useCurrentWorkspace();
  const detail = useAgentPresenceDetail(ws?.id, agentId);
  if (detail === "loading") return null;

  const { dotClass, label } = availabilityConfig[detail.availability];
  const px = size ? AVATAR_SIZE_PX[size] : 24;
  const dotSize = px >= 24 ? "h-1.5 w-1.5" : "h-1 w-1";

  return (
    <span
      aria-label={`Status: ${label}`}
      className={`absolute right-0 rounded-full ring-1 ring-background ${placement === "top" ? "top-0" : "bottom-0"} ${dotClass} ${dotSize}`}
    />
  );
}

function AgentIdentityAvatarFrame({
  badge,
  label,
  variant,
  hostMode,
  children,
}: {
  badge: AgentIdentityBadgeModel;
  label: string;
  variant: AgentIdentityBadgeVariant;
  hostMode: AgentIdentityBadgeHostMode;
  children: React.ReactNode;
}) {
  const frameRef = useRef<HTMLSpanElement>(null);
  const ownerDescriptionId = useId();
  const [ownerElement, setOwnerElement] = useState<HTMLElement | null>(null);
  const [ownerTooltipOpen, setOwnerTooltipOpen] = useState(false);

  useEffect(() => {
    if (hostMode !== "owned") return;
    const frame = frameRef.current;
    if (!frame) return;

    const owner = frame.parentElement?.closest(
      FOCUSABLE_ANCESTOR_SELECTOR,
    ) as HTMLElement | null;
    if (!owner) return;

    const previousAriaLabel = owner.getAttribute("aria-label");
    const previousAriaDescribedBy = owner.getAttribute("aria-describedby");
    const baseLabel = previousAriaLabel ?? getOwnerTextWithoutIdentityFrame(owner);
    owner.setAttribute("aria-label", appendIdentityLabel(baseLabel, label));
    owner.setAttribute(
      "aria-describedby",
      mergeIdRefs(previousAriaDescribedBy, ownerDescriptionId),
    );

    const open = () => setOwnerTooltipOpen(true);
    const close = () => setOwnerTooltipOpen(false);
    owner.addEventListener("focusin", open);
    owner.addEventListener("focusout", close);
    owner.addEventListener("mouseenter", open);
    owner.addEventListener("mouseleave", close);
    setOwnerElement(owner);

    return () => {
      owner.removeEventListener("focusin", open);
      owner.removeEventListener("focusout", close);
      owner.removeEventListener("mouseenter", open);
      owner.removeEventListener("mouseleave", close);
      restoreAttribute(owner, "aria-label", previousAriaLabel);
      restoreAttribute(owner, "aria-describedby", previousAriaDescribedBy);
      setOwnerElement(null);
      setOwnerTooltipOpen(false);
    };
  }, [hostMode, label, ownerDescriptionId]);

  const badgeElement = (
    <span
      data-testid="agent-identity-badge"
      aria-hidden="true"
      className={cn(
        "max-w-14 truncate border font-mono font-semibold uppercase tabular-nums tracking-normal forced-colors:border-[CanvasText] forced-colors:bg-[Canvas] forced-colors:text-[CanvasText]",
        badge.roleCode && isKnownRoleCode(badge.roleCode)
          ? ROLE_CLASS[badge.roleCode]
          : "border-border bg-muted text-muted-foreground",
        variant === "corner-tag" &&
          "absolute -bottom-0.5 -right-1 rounded px-0.5 text-[8px] leading-3 shadow-sm",
        variant === "chip-below" &&
          "absolute -bottom-3 left-1/2 -translate-x-1/2 rounded px-0.5 text-[8px] leading-3 shadow-sm",
        variant === "inline-row" &&
          "ml-1 inline-flex h-4 max-w-20 items-center rounded px-1 text-[10px] leading-none",
      )}
    >
      {badge.text}
    </span>
  );

  return (
    <span
      ref={frameRef}
      data-agent-identity-frame=""
      className={cn(
        "relative inline-flex shrink-0 items-center",
        variant === "chip-below" && "mb-3",
      )}
    >
      {children}
      {hostMode === "owned" && (
        <span id={ownerDescriptionId} className="sr-only">
          {label}
        </span>
      )}
      {hostMode === "standalone" ? (
        <Tooltip>
          <TooltipTrigger
            render={<span />}
            tabIndex={0}
            aria-label={label}
            className="inline-flex rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {badgeElement}
          </TooltipTrigger>
          <TooltipContent>{label}</TooltipContent>
        </Tooltip>
      ) : (
        <>
          {badgeElement}
          {ownerElement && ownerTooltipOpen ? (
            <Tooltip open={ownerTooltipOpen} onOpenChange={setOwnerTooltipOpen}>
              <TooltipContent anchor={ownerElement}>{label}</TooltipContent>
            </Tooltip>
          ) : null}
        </>
      )}
    </span>
  );
}

function displayIdentityCode(code: string, kind: "role" | "language") {
  const known = kind === "role"
    ? isKnownRoleCode(code)
    : isKnownLanguageCode(code);
  return known ? code : code.slice(0, 4);
}

function getOwnerTextWithoutIdentityFrame(owner: HTMLElement) {
  const clone = owner.cloneNode(true) as HTMLElement;
  clone.querySelectorAll("[data-agent-identity-frame]").forEach((element) => {
    element.remove();
  });
  return clone.textContent?.replace(/\s+/g, " ").trim() ?? "";
}

function appendIdentityLabel(baseLabel: string, identityLabel: string) {
  if (!baseLabel) return identityLabel;
  if (baseLabel.includes(identityLabel)) return baseLabel;
  return `${baseLabel}: ${identityLabel}`;
}

function mergeIdRefs(existing: string | null, next: string) {
  const ids = new Set((existing ?? "").split(/\s+/).filter(Boolean));
  ids.add(next);
  return Array.from(ids).join(" ");
}

function restoreAttribute(
  element: HTMLElement,
  name: "aria-label" | "aria-describedby",
  value: string | null,
) {
  if (value === null) {
    element.removeAttribute(name);
    return;
  }
  element.setAttribute(name, value);
}

function isKnownRoleCode(code: string): code is KnownRoleCode {
  return (ROLE_CODES as readonly string[]).includes(code);
}

function isKnownLanguageCode(code: string): code is KnownLanguageCode {
  return (LANGUAGE_CODES as readonly string[]).includes(code);
}

function formatAgentIdentityBadgeLabel(
  t: TFunction<"agents">,
  badge: AgentIdentityBadgeModel,
) {
  const role = badge.roleCode
    ? roleLabel(t, badge.roleCode)
    : t(($) => $.identity.role_unset);
  const languages =
    badge.languageCodes.length > 0
      ? badge.languageCodes.map((code) => languageLabel(t, code)).join(", ")
      : t(($) => $.identity.language_unset);
  return `${role} \u00b7 ${languages}`;
}

function roleLabel(t: TFunction<"agents">, code: string) {
  if (!isKnownRoleCode(code)) return code;
  const labels = {
    TL: t(($) => $.identity.roles.TL),
    BE: t(($) => $.identity.roles.BE),
    FE: t(($) => $.identity.roles.FE),
    FS: t(($) => $.identity.roles.FS),
    QA: t(($) => $.identity.roles.QA),
    OPS: t(($) => $.identity.roles.OPS),
    ML: t(($) => $.identity.roles.ML),
    DA: t(($) => $.identity.roles.DA),
    SRE: t(($) => $.identity.roles.SRE),
    SEC: t(($) => $.identity.roles.SEC),
  };
  return labels[code];
}

function languageLabel(t: TFunction<"agents">, code: string) {
  if (!isKnownLanguageCode(code)) return code;
  const labels = {
    GO: t(($) => $.identity.languages.GO),
    PY: t(($) => $.identity.languages.PY),
    TS: t(($) => $.identity.languages.TS),
    JS: t(($) => $.identity.languages.JS),
    RS: t(($) => $.identity.languages.RS),
    SH: t(($) => $.identity.languages.SH),
    RB: t(($) => $.identity.languages.RB),
    JV: t(($) => $.identity.languages.JV),
    KT: t(($) => $.identity.languages.KT),
    SW: t(($) => $.identity.languages.SW),
    CS: t(($) => $.identity.languages.CS),
    CP: t(($) => $.identity.languages.CP),
    SC: t(($) => $.identity.languages.SC),
    EL: t(($) => $.identity.languages.EL),
  };
  return labels[code];
}

/**
 * Wraps an agent avatar in a hover-card. The trigger is keyboard-focusable
 * only when no focusable ancestor (link/button) already provides a tab stop —
 * this prevents nested tabbable descendants and keyboard-nav bloat at sites
 * where the avatar lives inside a row link or click target.
 */
function AgentAvatarHoverCard({
  agentId,
  variant,
  children,
}: {
  agentId: string;
  variant: AgentHoverCardVariant;
  children: React.ReactNode;
}) {
  const content =
    variant === "live" ? (
      <AgentLivePeekCard agentId={agentId} />
    ) : (
      <AgentProfileCard agentId={agentId} />
    );
  return (
    <ActorAvatarHoverCardShell content={content}>
      {children}
    </ActorAvatarHoverCardShell>
  );
}

function MemberAvatarHoverCard({
  userId,
  children,
}: {
  userId: string;
  children: React.ReactNode;
}) {
  return (
    <ActorAvatarHoverCardShell content={<MemberProfileCard userId={userId} />}>
      {children}
    </ActorAvatarHoverCardShell>
  );
}

function SquadAvatarHoverCard({
  squadId,
  children,
}: {
  squadId: string;
  children: React.ReactNode;
}) {
  return (
    <ActorAvatarHoverCardShell content={<SquadProfileCard squadId={squadId} />}>
      {children}
    </ActorAvatarHoverCardShell>
  );
}

// Common chrome shared between agent and member hover cards. Keeps focus
// behaviour and width consistent so the two surfaces feel structurally
// parallel — content varies, frame doesn't.
//
// Do NOT defer-mount the HoverCard on pointerenter to save per-avatar mount
// cost (MUL-4827). Base UI drives hover through native mouseenter/mouseleave
// listeners on the trigger element, and installs its close path *inside* the
// mouseleave handler — so a trigger that never received a real mouseenter can
// neither cancel a pending open nor ever hover-close. Warming on pointerenter
// swaps the node mid-gesture and loses exactly those events, which made
// brushed-past avatars pop open ~600ms later and stick. This is the same
// invariant DeferredPopup documents: deferral is only sound for events that
// END a gesture (click/Enter), and hover starts one. Mounting the root eagerly
// costs ~0.15ms of JS per avatar and adds zero DOM while closed (the popup
// subtree, and its queries, stay unmounted until open).
function ActorAvatarHoverCardShell({
  content,
  children,
}: {
  content: React.ReactNode;
  children: React.ReactNode;
}) {
  const triggerRef = useRef<HTMLSpanElement>(null);
  const [standalone, setStandalone] = useState(false);

  useEffect(() => {
    const el = triggerRef.current;
    if (!el) return;
    const ancestor = el.parentElement?.closest(FOCUSABLE_ANCESTOR_SELECTOR);
    setStandalone(!ancestor);
  }, []);

  const tabIndex = standalone ? 0 : -1;
  const className = standalone
    ? "inline-flex cursor-pointer rounded-full focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    : "inline-flex cursor-pointer";

  return (
    <HoverCard>
      <HoverCardTrigger
        render={<span ref={triggerRef} />}
        tabIndex={tabIndex}
        className={className}
      >
        {children}
      </HoverCardTrigger>
      <HoverCardContent align="start" className="w-72">
        {content}
      </HoverCardContent>
    </HoverCard>
  );
}
