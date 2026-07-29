import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { MemoryAuditEvent, MemoryMutationResponse } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { MemoryAuditBoard } from "./memory-audit-board";

const deleteMemory = vi.hoisted(() => vi.fn());
const eraseMemory = vi.hoisted(() => vi.fn());
const auditState = vi.hoisted(() => ({
  current: {
    events: [] as MemoryAuditEvent[],
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    exportMemoryAudit: vi.fn(),
  },
}));

vi.mock("@multica/core/memory", () => ({
  memoryAuditOptions: () => ({ queryKey: ["memory", "audit"] }),
  useCorrectMemory: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useInvalidateMemory: () => ({ isPending: false, mutateAsync: vi.fn() }),
  useDeleteMemory: () => ({ isPending: false, mutateAsync: deleteMemory }),
  useEraseMemoryScope: () => ({ isPending: false, mutateAsync: eraseMemory }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    data: { events: auditState.current.events, next_cursor: null },
    isFetching: false,
    isLoading: false,
    refetch: vi.fn(),
  }),
}));

const event: MemoryAuditEvent = {
  id: "event-1",
  workspace_id: "ws-1",
  project_id: "project-12345678",
  issue_id: "issue-12345678",
  agent_id: "agent-1",
  actor_type: "member",
  source_type: "comment",
  source_id: "comment-1",
  event_type: "capture",
  text: "Remember the billing preference.",
  envelope: {},
  metadata: {},
  idempotency_key: "capture-1",
  actor_id: "member-1",
  status: "delivered",
  created_at: "2026-07-28T12:00:00Z",
  updated_at: "2026-07-28T12:00:01Z",
  deliveries: [
    {
      id: "delivery-1",
      provider: "mem0",
      status: "delivered",
      provider_memory_id: "mem-1234567890",
      response: {},
      error: null,
      attempt_count: 1,
      created_at: "2026-07-28T12:00:00Z",
      updated_at: "2026-07-28T12:00:01Z",
      delivery_lag_ms: 1000,
    },
    {
      id: "delivery-2",
      provider: "hindsight",
      status: "terminal_failed",
      provider_memory_id: "hind-1234567890",
      response: {},
      error: "provider rejected delete",
      attempt_count: 2,
      created_at: "2026-07-28T12:00:00Z",
      updated_at: "2026-07-28T12:00:02Z",
      terminal_at: "2026-07-28T12:00:02Z",
      delivery_lag_ms: 0,
    },
  ],
};

describe("MemoryPage", () => {
  beforeEach(() => {
    auditState.current.events = [event];
    deleteMemory.mockReset();
    eraseMemory.mockReset();
  });

  it("surfaces provider delivery failures in the audit table", () => {
    renderWithI18n(<MemoryAuditBoard />);

    expect(screen.getByText("mem0")).toBeInTheDocument();
    expect(screen.getByText("hindsight")).toBeInTheDocument();
    expect(screen.getByText("terminal_failed")).toBeInTheDocument();
    expect(screen.getByText("provider rejected delete")).toBeInTheDocument();
  });

  it("requires DELETE before a provider delete and shows partial provider results", async () => {
    const user = userEvent.setup();
    const response: MemoryMutationResponse = {
      operation: "delete",
      results: [
        { provider: "mem0", status: "delivered", provider_memory_id: "mem-1234567890" },
        { provider: "hindsight", status: "terminal_failed", provider_memory_id: "hind-1234567890", error: "provider rejected delete" },
      ],
    };
    deleteMemory.mockResolvedValue(response);

    renderWithI18n(<MemoryAuditBoard />);

    await user.click(screen.getAllByTitle("Delete")[0]!);
    const dialog = screen.getByRole("dialog", { name: "Delete memory" });
    const confirm = within(dialog).getByRole("button", { name: "Confirm" });

    expect(confirm).toBeDisabled();

    await user.type(within(dialog).getByPlaceholderText("DELETE"), "DELETE");
    await user.click(confirm);

    expect(deleteMemory).toHaveBeenCalledWith({
      eventId: "event-1",
      provider: "mem0",
      provider_memory_id: "mem-1234567890",
      reason: undefined,
      confirmation: "DELETE",
    });
    expect(await within(dialog).findByText(/Provider result 1\/2/)).toBeInTheDocument();
    expect(within(dialog).getByText("mem0")).toBeInTheDocument();
    expect(within(dialog).getByText("delivered")).toBeInTheDocument();
    expect(within(dialog).getByText("hindsight")).toBeInTheDocument();
    expect(within(dialog).getByText("terminal_failed")).toBeInTheDocument();
    expect(within(dialog).getByText("provider rejected delete")).toBeInTheDocument();
  });
  it("erases project and issue scopes with confirmation and filter ids", async () => {
    const user = userEvent.setup();
    const response: MemoryMutationResponse = {
      operation: "erase",
      results: [
        { provider: "mem0", status: "delivered", provider_memory_id: "mem-1234567890" },
        { provider: "hindsight", status: "terminal_failed", provider_memory_id: "hind-1234567890", error: "erase failed" },
      ],
    };
    eraseMemory.mockResolvedValue(response);

    renderWithI18n(<MemoryAuditBoard />);

    const projectErase = screen.getByRole("button", { name: "Erase project" });
    const issueErase = screen.getByRole("button", { name: "Erase issue" });
    expect(projectErase).toBeDisabled();
    expect(issueErase).toBeDisabled();

    await user.type(screen.getByPlaceholderText("Project ID"), "project-12345678");
    await user.type(screen.getByPlaceholderText("Issue ID"), "issue-12345678");
    await user.type(screen.getByPlaceholderText("ERASE"), "ERASE");

    expect(projectErase).toBeEnabled();
    expect(issueErase).toBeEnabled();

    await user.click(projectErase);
    expect(eraseMemory).toHaveBeenLastCalledWith({
      scope: "project",
      project_id: "project-12345678",
      issue_id: undefined,
      confirmation: "ERASE",
    });
    expect(await screen.findByText(/Provider result 1\/2/)).toBeInTheDocument();
    expect(screen.getByText("erase failed")).toBeInTheDocument();

    await user.click(issueErase);
    expect(eraseMemory).toHaveBeenLastCalledWith({
      scope: "issue",
      project_id: undefined,
      issue_id: "issue-12345678",
      confirmation: "ERASE",
    });
  });

});
