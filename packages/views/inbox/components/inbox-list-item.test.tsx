import { render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { InboxItem, Project } from "@multica/core/types";
import { InboxListItem } from "./inbox-list-item";

vi.mock("../../issues/components", () => ({ StatusIcon: () => null }));
vi.mock("../../issues/components/issue-agent-activity-indicator", () => ({
  IssueAgentActivityIndicator: ({
    issueId,
    hoverCard,
  }: {
    issueId: string;
    hoverCard?: boolean;
  }) => (
    <span
      data-testid="issue-agent-activity"
      data-issue-id={issueId}
      data-hover-card={hoverCard === false ? "false" : "true"}
    />
  ),
}));
vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({
    actorType,
    actorId,
    showStatusDot,
    identityBadge,
  }: {
    actorType: string;
    actorId: string;
    showStatusDot?: boolean;
    identityBadge?: { variant?: string; hostMode?: string } | boolean;
  }) => (
    <span
      data-testid="actor-avatar"
      data-actor-type={actorType}
      data-actor-id={actorId}
      data-show-status-dot={showStatusDot === true ? "true" : "false"}
      data-identity-variant={
        typeof identityBadge === "object" ? identityBadge.variant ?? "" : ""
      }
      data-identity-host-mode={
        typeof identityBadge === "object" ? identityBadge.hostMode ?? "" : ""
      }
    />
  ),
}));
vi.mock("./inbox-detail-label", () => ({ InboxDetailLabel: () => null }));
vi.mock("../../i18n", () => ({ useT: () => ({ t: () => "label" }) }));

function item(overrides: Partial<InboxItem> = {}): InboxItem {
  return {
    id: "inbox-1",
    workspace_id: "workspace-1",
    recipient_type: "member",
    recipient_id: "member-1",
    actor_type: "agent",
    actor_id: "agent-1",
    type: "new_comment",
    severity: "info",
    issue_id: "issue-1",
    project_id: null,
    title: "Issue title",
    body: null,
    issue_status: null,
    read: false,
    archived: false,
    created_at: "2026-06-15T08:00:00Z",
    details: null,
    ...overrides,
  };
}

function renderRow(props: {
  item: InboxItem;
  view: "inbox" | "archived";
  project?: Project;
}) {
  return render(
    <InboxListItem
      item={props.item}
      view={props.view}
      isSelected={false}
      project={props.project}
      onClick={vi.fn()}
      onAction={vi.fn()}
    />,
  );
}

function project(overrides: Partial<Project> = {}): Project {
  return {
    id: "proj-1",
    workspace_id: "workspace-1",
    title: "Launch",
    description: null,
    icon: null,
    status: "in_progress",
    priority: "none",
    lead_type: null,
    lead_id: null,
    start_date: null,
    due_date: null,
    created_at: "2026-06-01T00:00:00Z",
    updated_at: "2026-06-01T00:00:00Z",
    issue_count: 0,
    done_count: 0,
    resource_count: 0,
    ...overrides,
  };
}

const unreadDot = (container: HTMLElement) => container.querySelector(".bg-brand");
const title = (container: HTMLElement) => container.querySelector(".truncate");

describe("InboxListItem unread affordance", () => {
  it("marks an unread row in the main inbox", () => {
    const { container } = renderRow({ item: item({ read: false }), view: "inbox" });

    expect(unreadDot(container)).not.toBeNull();
    expect(title(container)?.className).toContain("font-medium");
  });

  it("leaves a read row unmarked in the main inbox", () => {
    const { container } = renderRow({ item: item({ read: true }), view: "inbox" });

    expect(unreadDot(container)).toBeNull();
    expect(title(container)?.className).not.toContain("font-medium");
  });

  it("renders an unread row as read in the archived view", () => {
    // Archiving preserves `read` so unarchiving can restore real unread state,
    // which left archived rows showing a dot no action in this view can clear.
    const { container } = renderRow({
      item: item({ read: false, archived: true }),
      view: "archived",
    });

    expect(unreadDot(container)).toBeNull();
    expect(title(container)?.className).not.toContain("font-medium");
  });
});

describe("InboxListItem issue activity", () => {
  it("shows issue-specific agent activity without an availability dot", () => {
    const { getByTestId } = renderRow({ item: item(), view: "inbox" });

    expect(getByTestId("actor-avatar").getAttribute("data-show-status-dot")).toBe(
      "false",
    );
    expect(
      getByTestId("issue-agent-activity").getAttribute("data-issue-id"),
    ).toBe("issue-1");
    expect(getByTestId("actor-avatar").getAttribute("data-identity-variant")).toBe(
      "corner-tag",
    );
    expect(getByTestId("actor-avatar").getAttribute("data-identity-host-mode")).toBe(
      "owned",
    );
  });

  it("shows the activity badge without its hover card", () => {
    // Triage rows only need "an agent is on this". The card behind the badge
    // adds elapsed time, which does not change whether you open the row, and
    // the row already carries the actor hover card on the left.
    const { getByTestId } = renderRow({ item: item(), view: "inbox" });

    expect(
      getByTestId("issue-agent-activity").getAttribute("data-hover-card"),
    ).toBe("false");
  });

  it("omits issue activity for a notification without an issue", () => {
    const { queryByTestId } = renderRow({
      item: item({ issue_id: null }),
      view: "inbox",
    });

    expect(queryByTestId("issue-agent-activity")).toBeNull();
  });
});

describe("InboxListItem project badge", () => {
  it("renders the green-accent project badge when a project is provided", () => {
    const { getByText, container } = renderRow({
      item: item({ project_id: "proj-1" }),
      view: "inbox",
      project: project(),
    });

    expect(getByText("Launch")).not.toBeNull();
    const badge = container.querySelector(".bg-success\\/10");
    expect(badge).not.toBeNull();
    expect(badge?.className).toContain("text-success");
  });

  it("caps and truncates the badge so it cannot crowd out title or detail", () => {
    const { container } = renderRow({
      item: item({ project_id: "proj-1" }),
      view: "inbox",
      project: project({ title: "A very long project title that must truncate" }),
    });

    const badge = container.querySelector(".bg-success\\/10");
    expect(badge?.className).toContain("max-w-[140px]");
    expect(badge?.className).toContain("shrink-0");
    expect(badge?.querySelector(".truncate")).not.toBeNull();
  });

  it("renders no badge when the item has no project", () => {
    const { container } = renderRow({ item: item(), view: "inbox" });

    expect(container.querySelector(".bg-success\\/10")).toBeNull();
  });

  it("renders no badge when project_id is set but unresolved", () => {
    const { container } = renderRow({
      item: item({ project_id: "proj-missing" }),
      view: "inbox",
    });

    expect(container.querySelector(".bg-success\\/10")).toBeNull();
  });
});
