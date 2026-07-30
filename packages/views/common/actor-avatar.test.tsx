/**
 * @vitest-environment jsdom
 */
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ActorAvatar, buildAgentIdentityBadgeModel } from "./actor-avatar";

const actorState = vi.hoisted(() => ({
  actorType: "agent",
  identity: { roleCode: "BE", languageCodes: ["GO"] },
  avatarUrl: "emoji:🤖",
}));

vi.mock("@multica/ui/components/common/actor-avatar", () => ({
  ActorAvatar: ({
    name,
    avatarUrl,
    ariaLabel,
  }: {
    name: string;
    avatarUrl?: string | null;
    ariaLabel?: string;
  }) => (
    <span
      data-testid="base-avatar"
      role={ariaLabel ? "img" : undefined}
      aria-label={ariaLabel}
      data-name={name}
      data-avatar-url={avatarUrl ?? ""}
    />
  ),
}));
vi.mock("@multica/ui/components/ui/hover-card", () => ({
  HoverCard: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  HoverCardTrigger: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
  HoverCardContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children, ...props }: React.HTMLAttributes<HTMLSpanElement>) => (
    <span {...props}>{children}</span>
  ),
  TooltipContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "Niko",
    getActorInitials: () => "N",
    getActorAvatarUrl: () => actorState.avatarUrl,
    getAgentIdentity: (type: string) => (type === "agent" ? actorState.identity : null),
  }),
}));
vi.mock("@multica/core/agents", () => ({
  useAgentPresenceDetail: () => ({ availability: "online" }),
}));
vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
    memberDetail: (id: string) => `/members/${id}`,
    squadDetail: (id: string) => `/squads/${id}`,
  }),
}));
vi.mock("../agents/presence", () => ({
  availabilityConfig: {
    online: { dotClass: "bg-green", label: "Online" },
  },
}));
vi.mock("../navigation", () => ({
  useNavigation: () => ({ push: vi.fn(), openInNewTab: vi.fn() }),
}));
vi.mock("../i18n", () => {
  const resources = {
    identity: {
      role_unset: "role unset",
      language_unset: "language unset",
      roles: {
        TL: "Tech Lead",
        BE: "Backend Engineer",
        FE: "Frontend Engineer",
        FS: "Full-Stack Engineer",
        QA: "Quality Assurance",
        OPS: "Operations / DevOps",
        ML: "Machine Learning",
        DA: "Data Analyst",
        SRE: "Site Reliability",
        SEC: "Security",
      },
      languages: {
        GO: "Go",
        PY: "Python",
        TS: "TypeScript",
        JS: "JavaScript",
        RS: "Rust",
        SH: "Shell",
        RB: "Ruby",
        JV: "Java",
        KT: "Kotlin",
        SW: "Swift",
        CS: "C#",
        CP: "C++",
        SC: "Scala",
        EL: "Elixir",
      },
    },
  };
  return { useT: () => ({ t: (selector: (r: typeof resources) => string) => selector(resources) }) };
});
vi.mock("../agents/components/agent-profile-card", () => ({ AgentProfileCard: () => null }));
vi.mock("../agents/components/agent-live-peek-card", () => ({ AgentLivePeekCard: () => null }));
vi.mock("../members/member-profile-card", () => ({ MemberProfileCard: () => null }));
vi.mock("../squads/components/squad-profile-card", () => ({ SquadProfileCard: () => null }));

describe("buildAgentIdentityBadgeModel", () => {
  it("renders role and language with the approved middle-dot separator", () => {
    expect(
      buildAgentIdentityBadgeModel(
        { roleCode: "be", languageCodes: ["go"] },
        "lg",
        "corner-tag",
      )?.text,
    ).toBe("BE\u00b7GO");
  });

  it("suppresses xs and uses compact role-only text at sm corner size", () => {
    const identity = { roleCode: "BE", languageCodes: ["GO"] };

    expect(buildAgentIdentityBadgeModel(identity, "xs", "corner-tag")).toBeNull();
    expect(buildAgentIdentityBadgeModel(identity, "sm", "corner-tag")?.text).toBe("BE");
  });

  it("bounds unknown values and renders polyglot counts deterministically", () => {
    expect(
      buildAgentIdentityBadgeModel(
        { roleCode: "specialist", languageCodes: ["go", "py", "ts", "go"] },
        "lg",
        "corner-tag",
      ),
    ).toMatchObject({ text: "SPECIA\u00b7+3", roleCode: "SPECIA", languageCodes: ["GO", "PY", "TS"] });
  });
});

describe("ActorAvatar agent identity badge", () => {
  it("renders an owned badge with localized title and combined accessible avatar name", () => {
    actorState.identity = { roleCode: "BE", languageCodes: ["GO"] };
    actorState.avatarUrl = "emoji:🤖";

    render(
      <ActorAvatar
        actorType="agent"
        actorId="agent-1"
        size="lg"
        profileLink={false}
        identityBadge={{ variant: "corner-tag", hostMode: "owned" }}
      />,
    );

    expect(screen.getByTestId("agent-identity-badge")).toHaveTextContent("BE\u00b7GO");
    expect(screen.getByTestId("agent-identity-badge")).toHaveAttribute(
      "title",
      "Backend Engineer \u00b7 Go",
    );
    expect(screen.getByRole("img", { name: "Niko: Backend Engineer \u00b7 Go" })).toBeTruthy();
    expect(screen.getByTestId("base-avatar")).toHaveAttribute("data-avatar-url", "");
  });

  it("does not render a badge for non-agent actors", () => {
    actorState.identity = { roleCode: "BE", languageCodes: ["GO"] };

    render(
      <ActorAvatar
        actorType="member"
        actorId="member-1"
        size="lg"
        profileLink={false}
        identityBadge
      />,
    );

    expect(screen.queryByTestId("agent-identity-badge")).toBeNull();
  });

  it("moves presence to the top-right when a corner badge is present", () => {
    actorState.identity = { roleCode: "QA", languageCodes: ["PY"] };

    render(
      <ActorAvatar
        actorType="agent"
        actorId="agent-1"
        size="md"
        profileLink={false}
        showStatusDot
        identityBadge
      />,
    );

    expect(screen.getByLabelText("Status: Online").className).toContain("top-0");
  });
});
