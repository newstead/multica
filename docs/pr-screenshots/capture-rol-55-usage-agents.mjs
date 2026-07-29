import { chromium } from '@playwright/test';

const baseUrl = process.env.ROL55_BASE_URL ?? 'http://127.0.0.1:3000';
const outputPath = process.env.ROL55_SCREENSHOT ?? 'docs/pr-screenshots/rol-55-usage-agents-seeded.png';
const now = '2026-07-29T12:00:00Z';
const workspace = {
  id: 'ws-rol-55',
  name: 'Agent Quality',
  slug: 'agent-quality-demo',
  description: 'Seeded workspace for the Usage Agents screenshot',
  context: null,
  settings: {},
  repos: [],
  issue_prefix: 'ROL',
  avatar_url: null,
  created_at: now,
  updated_at: now,
};
const user = {
  id: 'user-rol-55',
  name: 'Usage Reviewer',
  email: 'usage-reviewer@multica.local',
  avatar_url: null,
  onboarded_at: now,
  onboarding_questionnaire: {},
  starter_content_state: 'skipped_legacy',
  language: 'en',
  profile_description: '',
  timezone: 'UTC',
  created_at: now,
  updated_at: now,
};
const agents = [
  makeAgent('agent-aq-web', 'aq-web'),
  makeAgent('agent-aq-go', 'aq-go'),
  makeAgent('agent-aq-qa', 'aq-qa'),
  makeAgent('agent-aq-lead', 'aq-lead'),
];
const projects = [
  makeProject('project-agent-quality', 'Agent Quality Dashboard'),
  makeProject('project-ingest', 'Usage Ingest Pipeline'),
];
const daily = [
  makeDaily('2026-07-23', 'openai', 'gpt-5-codex', 830000, 260000, 410000, 90000, 14),
  makeDaily('2026-07-24', 'anthropic', 'claude-sonnet-4.5', 720000, 210000, 360000, 70000, 11),
  makeDaily('2026-07-25', 'openai', 'gpt-5-codex', 960000, 300000, 540000, 110000, 18),
  makeDaily('2026-07-26', 'google', 'gemini-2.5-pro', 380000, 125000, 160000, 42000, 6),
  makeDaily('2026-07-27', 'anthropic', 'claude-sonnet-4.5', 1140000, 360000, 620000, 150000, 21),
  makeDaily('2026-07-28', 'openai', 'gpt-5-codex', 1320000, 390000, 760000, 170000, 24),
  makeDaily('2026-07-29', 'cursor', 'auto', 690000, 205000, 310000, 88000, 10),
];
const byAgent = [
  makeByAgent('agent-aq-web', 'openai', 'gpt-5-codex', 1520000, 420000, 820000, 180000, 26),
  makeByAgent('agent-aq-web', 'anthropic', 'claude-sonnet-4.5', 760000, 260000, 360000, 70000, 9),
  makeByAgent('agent-aq-go', 'openai', 'gpt-5-codex', 1180000, 310000, 540000, 120000, 19),
  makeByAgent('agent-aq-go', 'google', 'gemini-2.5-pro', 640000, 180000, 280000, 60000, 8),
  makeByAgent('agent-aq-qa', 'anthropic', 'claude-haiku-4.5', 520000, 140000, 210000, 45000, 12),
  makeByAgent('agent-aq-qa', 'openai', 'gpt-5-mini', 410000, 110000, 180000, 38000, 10),
  makeByAgent('agent-aq-lead', 'openai', 'gpt-5-codex', 350000, 90000, 160000, 30000, 7),
  makeByAgent('agent-aq-lead', 'cursor', 'auto', 310000, 85000, 130000, 25000, 5),
  makeByAgent('agent-aq-web', 'xai', 'grok-code-fast-1', 260000, 62000, 90000, 18000, 4),
  makeByAgent('agent-aq-go', 'mistral', 'codestral-latest', 210000, 58000, 75000, 14000, 3),
  makeByAgent('agent-aq-qa', 'deepseek', 'deepseek-coder', 160000, 42000, 64000, 12000, 3),
  makeByAgent('agent-aq-lead', '', '', 120000, 32000, 36000, 8000, 2),
];
const sessions = [
  {
    agent_id: 'agent-aq-web',
    task_count: 18,
    completed_count: 14,
    failed_count: 4,
    failure_reasons: [
      { failure_reason: 'web build ENOSPC', count: 2 },
      { failure_reason: 'screenshot auth missing', count: 1 },
    ],
    queue_wait_p50_seconds: 92,
    queue_wait_p95_seconds: 420,
    run_duration_p50_seconds: 640,
    run_duration_p95_seconds: 1280,
  },
  {
    agent_id: 'agent-aq-go',
    task_count: 15,
    completed_count: 13,
    failed_count: 2,
    failure_reasons: [{ failure_reason: 'migration lock timeout', count: 2 }],
    queue_wait_p50_seconds: 58,
    queue_wait_p95_seconds: 210,
    run_duration_p50_seconds: 520,
    run_duration_p95_seconds: 940,
  },
  {
    agent_id: 'agent-aq-qa',
    task_count: 16,
    completed_count: 15,
    failed_count: 1,
    failure_reasons: [{ failure_reason: '', count: 1 }],
    queue_wait_p50_seconds: 44,
    queue_wait_p95_seconds: 130,
    run_duration_p50_seconds: 310,
    run_duration_p95_seconds: 720,
  },
  {
    agent_id: 'agent-aq-lead',
    task_count: 8,
    completed_count: 8,
    failed_count: 0,
    failure_reasons: [],
    queue_wait_p50_seconds: 36,
    queue_wait_p95_seconds: 95,
    run_duration_p50_seconds: 280,
    run_duration_p95_seconds: 650,
  },
];
const code = [
  makeCode('agent-aq-web', 4820, 1260, 44, 18, 5220, 1410, 52, 7),
  makeCode('agent-aq-go', 2100, 870, 33, 15, 1840, 720, 28, 5),
  makeCode('agent-aq-qa', 420, 180, 12, 16, 260, 95, 7, 2),
  makeCode('agent-aq-lead', 190, 75, 6, 8, 110, 32, 4, 1),
];

const browser = await chromium.launch();
const debug = process.env.ROL55_DEBUG === '1';
const page = await browser.newPage({ viewport: { width: 1440, height: 1860 }, deviceScaleFactor: 1 });
if (debug) {
  page.on('console', (msg) => console.log('console', msg.type(), msg.text()));
  page.on('pageerror', (err) => console.log('pageerror', err.stack || err.message));
  page.on('requestfailed', (req) => console.log('requestfailed', req.method(), req.url(), req.failure()?.errorText));
}
await page.context().addCookies([
  { name: 'multica_logged_in', value: '1', url: baseUrl },
  { name: 'last_workspace_slug', value: workspace.slug, url: baseUrl },
]);
await page.route('**/api/**', async (route) => {
  const request = route.request();
  const url = new URL(request.url());
  const pathname = url.pathname;
  if (debug) console.log('api', route.request().method(), pathname);
  const method = request.method();
  const json = (body, status = 200) => route.fulfill({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  });

  if (pathname === '/api/config') return json({ feature_flags: {} });
  if (pathname === '/api/me') return json(user);
  if (pathname === '/api/workspaces') return json(method === 'GET' ? [workspace] : workspace);
  if (pathname === `/api/workspaces/${workspace.id}/members`) {
    return json([{ id: 'member-rol-55', workspace_id: workspace.id, user_id: user.id, role: 'owner', created_at: now, name: user.name, email: user.email, avatar_url: null }]);
  }
  if (pathname === '/api/agents') return json(agents);
  if (pathname === '/api/projects') return json({ projects, total: projects.length });
  if (pathname === '/api/dashboard/usage/daily') return json(daily);
  if (pathname === '/api/dashboard/usage/by-agent') return json(byAgent);
  if (pathname === '/api/dashboard/agents/sessions') return json(sessions);
  if (pathname === '/api/dashboard/agents/code') return json(code);
  if (pathname === '/api/inbox/unread-summary') return json([]);
  if (pathname === '/api/inbox/unread-count') return json({ count: 0 });
  if (pathname === '/api/notification-preferences') return json({ preferences: {} });
  if (pathname === '/api/squads') return json([]);
  if (pathname === '/api/client-usage') return json({ ok: true });
  if (pathname === '/api/chat/sessions') return json([]);
  if (pathname === '/api/chat/pending-tasks') return json([]);
  if (pathname === '/api/chat/pending-tasks/has-any') return json({ has_any: false });
  if (pathname.startsWith('/api/chat')) return json([]);
  if (pathname === '/api/issues') {
    return json({ issues: [], total: 0 });
  }
  if (pathname === '/api/runtimes') return json([]);
  if (pathname === '/api/agent-task-snapshot') return json([]);
  if (pathname === '/api/invitations') return json([]);
  if (pathname === '/api/inbox') return json([]);
  if (pathname === '/api/pins') return json([]);
  return json(method === 'GET' ? [] : { ok: true });
});

await page.goto(`${baseUrl}/${workspace.slug}/usage/agents`, { waitUntil: 'domcontentloaded', timeout: 60_000 });
await page.getByRole('heading', { name: 'Bottlenecks' }).waitFor({ timeout: 30_000 }).catch(async (error) => {
  if (debug) {
    console.log('url', page.url());
    const bodyText = await page.locator('body').innerText().catch(() => '');
    console.log('body length', bodyText.length);
    console.log(bodyText.slice(0, 4000));
    await page.screenshot({ path: 'docs/pr-screenshots/debug-rol-55-seeded.png', fullPage: true });
  }
  throw error;
});
await page.locator('text=aq-web').first().waitFor({ timeout: 30_000 });
await page.screenshot({ path: outputPath, fullPage: true });
await browser.close();
console.log(`Captured ${outputPath} from ${baseUrl}/${workspace.slug}/usage/agents with Playwright API route interception.`);

function makeAgent(id, name) {
  return {
    id,
    workspace_id: workspace.id,
    runtime_id: `${id}-runtime`,
    name,
    description: '',
    instructions: '',
    avatar_url: null,
    status: 'idle',
    runtime_mode: 'cloud',
    runtime_config: {},
    custom_args: [],
    visibility: 'workspace',
    permission_mode: 'public_to',
    invocation_targets: [{ target_type: 'workspace', target_id: null }],
    max_concurrent_tasks: 3,
    model: 'gpt-5-codex',
    owner_id: null,
    skills: [],
    created_at: now,
    updated_at: now,
    archived_at: null,
    archived_by: null,
  };
}
function makeProject(id, title) {
  return {
    id,
    workspace_id: workspace.id,
    title,
    description: null,
    icon: 'folder-kanban',
    status: 'in_progress',
    priority: 'high',
    lead_type: 'agent',
    lead_id: 'agent-aq-web',
    start_date: '2026-07-01',
    due_date: null,
    created_at: now,
    updated_at: now,
    issue_count: 12,
    done_count: 7,
    resource_count: 2,
  };
}
function makeDaily(date, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, task_count) {
  return { date, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd_ticks: 0, task_count };
}
function makeByAgent(agent_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, task_count) {
  return { agent_id, provider, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd_ticks: 0, task_count };
}
function makeCode(agent_id, additions, deletions, files_changed, task_count, pr_additions, pr_deletions, pr_changed_files, pull_request_count) {
  return { agent_id, additions, deletions, files_changed, task_count, pr_additions, pr_deletions, pr_changed_files, pull_request_count };
}
