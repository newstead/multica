import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { MemoryAuditEvent, MemoryMutationResponse } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { MemoryPage } from "./memory-page";

const deleteMemory = vi.hoisted(() => vi.fn());
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
  useEraseMemoryScope: () => ({ isPending: false, mutateAsync: vi.fn() }),
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
  });

  it("surfaces provider delivery failures in the audit table", () => {
    renderWithI18n(<MemoryPage />);

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

    renderWithI18n(<MemoryPage />);

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
  });
});
