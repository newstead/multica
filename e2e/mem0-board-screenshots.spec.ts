import { mkdirSync } from "node:fs";
import { expect, type Page, type Route, test } from "@playwright/test";

const SHOTS_DIR = process.env.MEM0_SHOTS_DIR ?? "/tmp/mem0-board-shots";
const WORKSPACE_ID = "ws-mem0-shots";
const WORKSPACE_SLUG = "mem0-shots";
const USER_ID = "user-mem0-shots";
const PROJECT_ID = "project-memory-gateway";
const NOW = Date.now();

test.use({ viewport: { width: 1440, height: 1000 } });

type Scenario = "normal" | "loading" | "empty" | "error";

function isoDaysAgo(days: number): string {
  return new Date(NOW - days * 24 * 60 * 60 * 1000).toISOString();
}

function json(data: unknown, status = 200) {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify(data),
  };
}

function workspace() {
  const now = isoDaysAgo(0);
  return {
    id: WORKSPACE_ID,
    name: "Mem0 Evidence",
    slug: WORKSPACE_SLUG,
    description: null,
    context: null,
    settings: {},
    repos: [],
    issue_prefix: "MEM",
    avatar_url: null,
    created_at: now,
    updated_at: now,
  };
}

function user() {
  const now = isoDaysAgo(0);
  return {
    id: USER_ID,
    name: "Memory QA",
    email: "memory-qa@example.test",
    avatar_url: null,
    onboarded_at: now,
    onboarding_questionnaire: {},
    starter_content_state: "imported",
    language: "en",
    profile_description: "",
    timezone: "UTC",
    created_at: now,
    updated_at: now,
  };
}

function member() {
  return {
    id: "member-mem0-shots",
    workspace_id: WORKSPACE_ID,
    user_id: USER_ID,
    role: "admin",
    name: "Memory QA",
    email: "memory-qa@example.test",
    avatar_url: null,
    created_at: isoDaysAgo(0),
  };
}

function project() {
  const now = isoDaysAgo(0);
  return {
    id: PROJECT_ID,
    workspace_id: WORKSPACE_ID,
    title: "Memory gateway",
    description: "Seeded mem0 visual evidence project",
    icon: "M",
    status: "in_progress",
    priority: "high",
    lead_type: "member",
    lead_id: USER_ID,
    start_date: null,
    due_date: null,
    created_at: now,
    updated_at: now,
    issue_count: 8,
    done_count: 5,
    resource_count: 2,
  };
}

function memoryConfig() {
  return {
    workspace_id: WORKSPACE_ID,
    enabled: true,
    primary_provider: "hindsight",
    shadow_provider: "mem0",
    read_mode: "dual",
    provider_settings: {},
    created_at: isoDaysAgo(8),
    updated_at: isoDaysAgo(0),
  };
}

function delivery(id: string, eventType: string, status: string, daysAgo: number, lagMs: number) {
  const ts = isoDaysAgo(daysAgo);
  return {
    id,
    workspace_id: WORKSPACE_ID,
    memory_event_id: `event-${id}`,
    project_id: PROJECT_ID,
    agent_id: "agent-memory-gateway",
    issue_id: `issue-${id}`,
    task_id: `task-${id}`,
    event_type: eventType,
    provider: "mem0",
    status,
    attempt_count: status === "terminal_failed" ? 3 : 1,
    delivery_lag_ms: lagMs,
    event_created_at: ts,
    delivery_created_at: ts,
    last_attempt_at: ts,
    terminal_at: status === "terminal_failed" ? ts : undefined,
    updated_at: ts,
  };
}

function recall(id: string, query: string, daysAgo: number, resultCount: number) {
  return {
    id,
    workspace_id: WORKSPACE_ID,
    project_id: PROJECT_ID,
    agent_id: "agent-memory-gateway",
    issue_id: `issue-${id}`,
    task_id: `task-${id}`,
    provider: "mem0",
    read_mode: "dual",
    recall_correlation_id: `recall-${id}`,
    query,
    results: Array.from({ length: resultCount }, (_, index) => ({ id: `${id}-memory-${index}` })),
    provenance: { provider: "mem0", items: resultCount },
    sampled_at: isoDaysAgo(daysAgo),
  };
}

function normalBoard() {
  return {
    health: { provider: "mem0", ok: true },
    deliveries: [
      delivery("retain-fast", "retain", "delivered", 0, 84),
      delivery("retain-slow", "retain", "delivered", 1, 235),
      delivery("update-ok", "update", "delivered", 2, 141),
      delivery("delete-failed", "delete", "terminal_failed", 3, 0),
    ],
    recall_samples: [
      recall("search-context", "Find workspace memory gateway decisions", 0, 3),
      recall("search-agent", "Recall agent-specific mem0 scope", 1, 2),
      recall("search-empty", "Check deleted memory history", 4, 0),
    ],
  };
}

async function installMocks(page: Page, scenario: Scenario): Promise<() => void> {
  let releaseLoading: (() => void) | undefined;
  await page.route("**/api/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;

    if (path === "/api/config") {
      await route.fulfill(json({
        cdn_domain: "",
        cdn_signed: false,
        allow_signup: true,
        google_client_id: "",
        daemon_server_url: "",
        daemon_app_url: "",
        workspace_creation_disabled: false,
        vcs_integration_available: false,
        feature_flags: {},
      }));
      return;
    }
    if (path === "/api/me") {
      await route.fulfill(json(user()));
      return;
    }
    if (path === "/api/workspaces") {
      await route.fulfill(json([workspace()]));
      return;
    }
    if (path === `/api/workspaces/${WORKSPACE_ID}/members`) {
      await route.fulfill(json([member()]));
      return;
    }
    if (path === "/api/projects") {
      await route.fulfill(json({ projects: [project()], total: 1 }));
      return;
    }
    if (path === `/api/workspaces/${WORKSPACE_ID}/memory/config`) {
      await route.fulfill(json(memoryConfig()));
      return;
    }
    if (path === `/api/workspaces/${WORKSPACE_ID}/memory/mem0-board`) {
      if (scenario === "loading") {
        await new Promise<void>((resolve) => { releaseLoading = resolve; });
        await route.fulfill(json(normalBoard()));
        return;
      }
      if (scenario === "empty") {
        await route.fulfill(json({ health: { provider: "mem0", ok: true }, deliveries: [], recall_samples: [] }));
        return;
      }
      if (scenario === "error") {
        await route.fulfill(json({ error: "seeded mem0 board failure" }, 500));
        return;
      }
      await route.fulfill(json(normalBoard()));
      return;
    }

    await fulfillBackgroundRoute(route, path);
  });
  return () => releaseLoading?.();
}

async function fulfillBackgroundRoute(route: Route, path: string) {
  if (path === "/api/invitations" || path === "/api/inbox" || path === "/api/inbox/archived") {
    await route.fulfill(json([]));
    return;
  }
  if (path === "/api/inbox/unread-summary" || path === "/api/pins" || path === "/api/chat/sessions") {
    await route.fulfill(json([]));
    return;
  }
  if (path === "/api/chat/pinned-agents") {
    await route.fulfill(json([]));
    return;
  }
  if (path === "/api/chat/pending-tasks/has-any") {
    await route.fulfill(json({ has_pending: false }));
    return;
  }
  if (path === "/api/chat/pending-tasks") {
    await route.fulfill(json({ tasks: [] }));
    return;
  }
  if (path === "/api/agents") {
    await route.fulfill(json([]));
    return;
  }
  if (path === "/api/runtimes" || path === "/api/squads" || path === "/api/skills") {
    await route.fulfill(json([]));
    return;
  }
  await route.fulfill(json({}));
}

async function openMem0Page(page: Page, scenario: Scenario) {
  const releaseLoading = await installMocks(page, scenario);
  await page.addInitScript(() => {
    localStorage.setItem("multica_token", "mem0-screenshot-token");
    localStorage.setItem("multica:chat:isOpen", "false");
  });
  await page.goto(`/${WORKSPACE_SLUG}/usage/mem0`, { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "mem0 memory" })).toBeVisible({ timeout: 30000 });
  return releaseLoading;
}

test.beforeAll(() => {
  mkdirSync(SHOTS_DIR, { recursive: true });
});

test("mem0 board screenshot evidence - seeded populated board", async ({ page }) => {
  await openMem0Page(page, "normal");
  await expect(page.getByText("Request audit")).toBeVisible();
  await expect(page.getByText("Find workspace memory gateway decisions")).toBeVisible();
  await page.screenshot({ path: `${SHOTS_DIR}/01-populated.png`, fullPage: true });
});

test("mem0 board screenshot evidence - loading state", async ({ page }) => {
  const releaseLoading = await openMem0Page(page, "loading");
  await expect(page.locator(".animate-pulse").first()).toBeVisible();
  await page.screenshot({ path: `${SHOTS_DIR}/02-loading.png`, fullPage: true });
  releaseLoading();
});

test("mem0 board screenshot evidence - empty state", async ({ page }) => {
  await openMem0Page(page, "empty");
  await expect(page.getByText("No mem0 recall or delivery rows in this window.").first()).toBeVisible();
  await expect(page.getByText("No mem0 audit rows in this window.")).toBeVisible();
  await page.screenshot({ path: `${SHOTS_DIR}/03-empty.png`, fullPage: true });
});

test("mem0 board screenshot evidence - error state", async ({ page }) => {
  await openMem0Page(page, "error");
  await expect(page.getByText("Memory data did not load")).toBeVisible({ timeout: 30000 });
  await page.screenshot({ path: `${SHOTS_DIR}/04-error.png`, fullPage: true });
});

test("mem0 board screenshot evidence - narrow viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 900 });
  await openMem0Page(page, "normal");
  await expect(page.getByText("Request audit")).toBeVisible();
  await page.screenshot({ path: `${SHOTS_DIR}/05-narrow.png`, fullPage: true });
});
