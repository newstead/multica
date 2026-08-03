// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";
import type {
  Agent,
  AgentRuntime,
  WorkspaceRolePolicy,
} from "@multica/core/types";
import { RolePolicySection } from "./role-policy-editor";

const mockGet = vi.hoisted(() => vi.fn());
const mockPut = vi.hoisted(() => vi.fn());
const policyRef = vi.hoisted(() => ({
  current: { enabled: false, rules: {} } as WorkspaceRolePolicy,
}));
const agentsRef = vi.hoisted(() => ({ current: [] as Agent[] }));
const runtimesRef = vi.hoisted(() => ({ current: [] as AgentRuntime[] }));
const modelsRef = vi.hoisted(() => ({
  current: [] as { id: string; label: string }[],
}));

vi.mock("@multica/core/api", () => ({
  api: {
    getWorkspaceRolePolicy: (...args: unknown[]) => mockGet(...args),
    updateWorkspaceRolePolicy: (...args: unknown[]) => mockPut(...args),
  },
}));

vi.mock("@multica/core/workspace/queries", () => ({
  workspaceRolePolicyOptions: (wsId: string) => {
    mockGet(wsId);
    return {
      queryKey: ["workspaces", wsId, "role-policy"],
      queryFn: () => policyRef.current,
    };
  },
  agentListOptions: (wsId: string) => ({
    queryKey: ["workspaces", wsId, "agents"],
    queryFn: () => agentsRef.current,
  }),
  workspaceKeys: { rolePolicy: (wsId: string) => ["workspaces", wsId, "role-policy"] },
}));

vi.mock("@multica/core/runtimes", () => ({
  runtimeListOptions: (wsId: string) => ({
    queryKey: ["runtimes", wsId, "list"],
    queryFn: () => runtimesRef.current,
  }),
  runtimeModelsOptions: (runtimeId: string) => ({
    queryKey: ["runtimes", "models", runtimeId],
    queryFn: () => ({ models: modelsRef.current, supported: true }),
  }),
}));

// Synchronous query data so the editor hydrates its draft before the test
// interacts with it — the same determinism the react-query mock gives
// workspace-tab.test.tsx.
vi.mock("@tanstack/react-query", () => ({
  useQuery: (options: { queryKey: readonly unknown[] }) => {
    const key = options.queryKey as readonly unknown[];
    if (key[0] === "workspaces" && key[2] === "role-policy") {
      return { data: policyRef.current, isFetched: true };
    }
    if (key[0] === "workspaces" && key[2] === "agents") {
      return { data: agentsRef.current, isFetched: true };
    }
    if (key[0] === "runtimes" && key[1] === "models") {
      return { data: { models: modelsRef.current, supported: true }, isFetched: true };
    }
    if (key[0] === "runtimes") {
      return { data: runtimesRef.current, isFetched: true };
    }
    return { data: undefined, isFetched: true };
  },
  useQueryClient: () => ({ setQueryData: vi.fn() }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

const TEST_RESOURCES = { en: { common: enCommon, settings: enSettings } };

function renderEditor(canEdit = true) {
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <RolePolicySection workspaceId="ws-1" canEdit={canEdit} />
    </I18nProvider>,
  );
}

function makeAgent(overrides: Partial<Agent>): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    name: "Default Agent",
    description: "",
    instructions: "",
    avatar_url: null,
    skills: [],
    owner_id: "user-1",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  } as Agent;
}

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Runtime 1",
    runtime_mode: "cloud",
    provider: "codex",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-01-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  } as AgentRuntime;
}

async function pickOption(comboboxName: string, optionLabel: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("combobox", { name: comboboxName }));
  await user.click(await screen.findByRole("option", { name: optionLabel }));
  return user;
}

async function save() {
  await userEvent.click(await screen.findByRole("button", { name: "Save" }));
}

describe("RolePolicySection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    policyRef.current = { enabled: false, rules: {} };
    agentsRef.current = [];
    runtimesRef.current = [];
    modelsRef.current = [];
  });

  it("renders all 10 canonical roles with their labels", () => {
    renderEditor();
    expect(screen.getByText("Lead")).toBeTruthy();
    for (const code of ["TL", "BE", "FE", "FS", "QA", "OPS", "ML", "DA", "SRE", "SEC"]) {
      expect(screen.getByRole("combobox", { name: `${code} Mode` })).toBeTruthy();
      expect(screen.getByRole("combobox", { name: `${code} Fallback` })).toBeTruthy();
    }
    expect(mockGet).toHaveBeenCalledWith("ws-1");
  });

  it("toggling enabled and saving PUTs the full matrix", async () => {
    renderEditor();
    await userEvent.click(screen.getByRole("switch", { name: "Enable role policy" }));
    await save();
    await waitFor(() => {
      expect(mockPut).toHaveBeenCalledWith("ws-1", { enabled: true, rules: {} });
    });
  });

  it("binds a role to an agent and saves agent_id", async () => {
    agentsRef.current = [
      makeAgent({ id: "agent-qa", name: "QA Bot", runtime_id: "rt-1" }),
    ];
    renderEditor();
    await pickOption("BE Mode", "Agent");
    await pickOption("BE Agent", "QA Bot");
    await save();
    await waitFor(() => {
      expect(mockPut).toHaveBeenCalledWith("ws-1", {
        enabled: false,
        rules: { BE: { agent_id: "agent-qa", fallback: "agent_default" } },
      });
    });
  });

  it("persists an exec-config rule (runtime + model)", async () => {
    runtimesRef.current = [makeRuntime({ id: "rt-1", name: "Runtime 1" })];
    modelsRef.current = [{ id: "gpt-5.6-sol", label: "GPT-5.6 Sol" }];
    renderEditor();
    await pickOption("FE Mode", "Execution config");
    await pickOption("FE Runtime", "Runtime 1");
    await pickOption("FE Model", "GPT-5.6 Sol");
    await save();
    await waitFor(() => {
      expect(mockPut).toHaveBeenCalledWith("ws-1", {
        enabled: false,
        rules: { FE: { runtime_id: "rt-1", model: "gpt-5.6-sol", fallback: "agent_default" } },
      });
    });
  });

  it("warns when enabled with an empty matrix", () => {
    policyRef.current = { enabled: true, rules: {} };
    renderEditor();
    expect(screen.getByText(/no roles have rules/i)).toBeTruthy();
  });

  it("loads an existing agent-bound rule", () => {
    policyRef.current = {
      enabled: true,
      rules: { TL: { agent_id: "agent-lead", fallback: "agent_default" } },
    };
    agentsRef.current = [
      makeAgent({ id: "agent-lead", name: "Lead Bot", runtime_id: "rt-1" }),
    ];
    renderEditor();
    const trigger = screen.getByRole("combobox", { name: "TL Agent" });
    expect(trigger).toHaveTextContent("Lead Bot");
  });

  it("is read-only for non-owner/admin members", () => {
    renderEditor(false);
    const toggle = screen.getByRole("switch", { name: "Enable role policy" });
    expect(toggle).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByRole("combobox", { name: "BE Mode" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    expect(screen.getByText(/only admins and owners/i)).toBeTruthy();
  });
});
