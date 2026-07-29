import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { renderWithI18n } from "../../test/i18n";

const dailyRowsRef = vi.hoisted(() => ({
  current: [] as import("@multica/core/types").DashboardUsageDaily[],
}));
const byAgentRowsRef = vi.hoisted(() => ({
  current: [] as import("@multica/core/types").DashboardUsageByAgent[],
}));
const chartRowsRef = vi.hoisted(() => ({
  current: [] as unknown[],
}));
const tzRef = vi.hoisted(() => ({ current: "UTC" }));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: (opts: { queryKey: readonly unknown[] }) => {
      const key = opts.queryKey;
      if (key[0] === "projects") {
        return { data: [], isLoading: false, isSuccess: true };
      }
      if (key[0] === "workspaces" && key[2] === "agents") {
        return { data: [], isLoading: false, isSuccess: true };
      }
      if (key[0] === "dashboard") {
        const kind = key[2];
        const data =
          kind === "daily"
            ? dailyRowsRef.current
            : kind === "by-agent"
              ? byAgentRowsRef.current
              : [];
        return { data, isLoading: false, isSuccess: true };
      }
      return { data: [], isLoading: false, isSuccess: true };
    },
  };
});

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  paths: {
    workspace: () => ({
      usage: () => "/acme/usage",
      usageAgents: () => "/acme/usage/agents",
    }),
  },
  useWorkspaceSlug: () => null,
}));

vi.mock("@multica/core/runtimes/custom-pricing-store", () => {
  const state = () => ({ pricings: {} });
  const useCustomPricingStore = Object.assign(
    (sel?: (s: ReturnType<typeof state>) => unknown) =>
      sel ? sel(state()) : state(),
    { getState: state },
  );
  return { useCustomPricingStore };
});

vi.mock("../../common/use-viewing-timezone", () => ({
  useViewingTimezone: () => tzRef.current,
}));

vi.mock("@multica/ui/components/ui/number-flow", () => ({
  NumberFlow: ({
    value,
    suffix = "",
  }: {
    value: number;
    suffix?: string;
  }) => <span>{Math.round(value)}{suffix}</span>,
  CompactNumberFlow: ({ value }: { value: number }) => <span>{Math.round(value)}</span>,
  CurrencyNumberFlow: ({ value }: { value: number }) => (
    <span>{`$${value.toFixed(2)}`}</span>
  ),
  NumberFlowGroup: ({ children }: { children: ReactNode }) => <>{children}</>,
}));

vi.mock("../../runtimes/components/charts", () => ({
  DailyTokensChart: ({ data }: { data: unknown[] }) => {
    chartRowsRef.current = data;
    return <pre aria-label="daily token chart">{JSON.stringify(data)}</pre>;
  },
}));

import { AgentsUsagePage } from "./agents-page";

function dailyRow(
  date: string,
  input: number,
  cacheRead: number,
): import("@multica/core/types").DashboardUsageDaily {
  return {
    date,
    provider: "anthropic",
    model: "claude-sonnet-4-6",
    input_tokens: input,
    output_tokens: 0,
    cache_read_tokens: cacheRead,
    cache_write_tokens: 0,
    task_count: 1,
  };
}

describe("AgentsUsagePage daily usage window", () => {
  beforeEach(() => {
    cleanup();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-29T12:00:00Z"));
    tzRef.current = "UTC";
    chartRowsRef.current = [];
    dailyRowsRef.current = [
      dailyRow("2026-07-28", 99_000_000, 99_000_000),
      dailyRow("2026-07-29", 1_000_000, 1_000_000),
    ];
    byAgentRowsRef.current = [
      {
        agent_id: "agent-1",
        provider: "anthropic",
        model: "claude-sonnet-4-6",
        input_tokens: 2_000_000,
        output_tokens: 0,
        cache_read_tokens: 0,
        cache_write_tokens: 0,
        task_count: 1,
      },
    ];
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("trims the API headroom day before computing Agents daily KPIs and chart rows", async () => {
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    renderWithI18n(<AgentsUsagePage />);

    await user.click(screen.getByRole("button", { name: "1d" }));

    await waitFor(() => {
      expect(chartRowsRef.current).toEqual([
        {
          date: "2026-07-29",
          label: "7/29",
          input: 1_000_000,
          output: 0,
          cacheRead: 1_000_000,
          cacheWrite: 0,
        },
      ]);
    });
    expect(screen.getByText("$3.30")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("1M cache reads")).toBeInTheDocument();
    expect(screen.getByText("anthropic/claude-sonnet-4-6")).toBeInTheDocument();
  });
});
