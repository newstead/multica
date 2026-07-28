-- ROL-33 data repair for agent-quality acceptance.
--
-- 1. Persist static-rate-table costs for historical task_usage rows whose
--    daemon report had no provider cost but whose model is priced by the same
--    table used for multica_llm_cost_usd_total.
-- 2. Seed the known merged ROL epic GitHub PR rows from gh CLI metadata and
--    link them to matching issue identifiers so PR-level LOC appears even on
--    workspaces where GitHub webhooks were not configured during the epic.
--
-- Marker tables make the data repair reversible without deleting provider-
-- reported costs or real webhook rows that may arrive later.

CREATE TABLE IF NOT EXISTS migration_225_task_usage_cost_backfill (
    task_usage_id UUID NOT NULL
);

CREATE TABLE IF NOT EXISTS migration_225_github_pr_backfill (
    pull_request_id UUID NOT NULL
);

CREATE TABLE IF NOT EXISTS migration_225_issue_pr_backfill (
    issue_id UUID NOT NULL,
    pull_request_id UUID NOT NULL
);

COMMENT ON COLUMN task_usage.cost_usd_ticks IS
    'Cost in 1e-10 USD. Positive provider-reported costs are stored as-is; when absent, the server may persist a static-rate-table estimate for priced models. NULL means no provider cost and no matching server price.';

DO $$
DECLARE
    v_started_at TIMESTAMPTZ := clock_timestamp();
BEGIN
    WITH rates(pattern, input_per_m, output_per_m, cache_read_per_m, cache_write_per_m) AS (
        VALUES
            ('(^|/|:)gpt-5\.6-sol$', 5.00::numeric, 30.00::numeric, 0.50::numeric, 6.25::numeric),
            ('(^|/|:)gpt-5\.6-terra$', 2.50::numeric, 15.00::numeric, 0.25::numeric, 3.125::numeric),
            ('(^|/|:)gpt-5\.6-luna$', 1.00::numeric, 6.00::numeric, 0.10::numeric, 1.25::numeric),
            ('(^|/|:)gpt-5[.-]5$|^gpt-5-5$', 5.00::numeric, 30.00::numeric, 0.50::numeric, 0.50::numeric),
            ('(^|/|:)gpt-5[.-]4($|-2026-03-05|-xhigh)', 2.50::numeric, 15.00::numeric, 0.25::numeric, 0.25::numeric),
            ('(^|/|:)gpt-5[.-]4-mini($|[^a-z0-9])', 0.75::numeric, 4.50::numeric, 0.075::numeric, 0.075::numeric),
            ('(^|/|:)gpt-5[.-]3-codex$', 1.75::numeric, 14.00::numeric, 0.175::numeric, 0.175::numeric),
            ('(^|/|:)gpt-5[.-]2-codex$', 1.75::numeric, 14.00::numeric, 0.175::numeric, 0.175::numeric),
            ('claude-sonnet-5|claude-5-sonnet', 2.00::numeric, 10.00::numeric, 0.20::numeric, 2.50::numeric),
            ('claude-fable-5', 10.00::numeric, 50.00::numeric, 1.00::numeric, 12.50::numeric),
            ('claude-opus-5', 5.00::numeric, 25.00::numeric, 0.50::numeric, 6.25::numeric),
            ('claude-opus-4[-.]8', 5.00::numeric, 25.00::numeric, 0.50::numeric, 6.25::numeric),
            ('claude-opus-4[-.]7', 5.00::numeric, 25.00::numeric, 0.50::numeric, 6.25::numeric),
            ('claude-opus-4[-.]6', 5.00::numeric, 25.00::numeric, 0.50::numeric, 6.25::numeric),
            ('claude-opus-4[-.]5', 5.00::numeric, 25.00::numeric, 0.50::numeric, 6.25::numeric),
            ('claude-sonnet-4[-.]6|claude-4[-.]6-sonnet', 3.00::numeric, 15.00::numeric, 0.30::numeric, 3.75::numeric),
            ('claude-sonnet-4[-.]5|claude-4[-.]5-sonnet', 3.00::numeric, 15.00::numeric, 0.30::numeric, 3.75::numeric),
            ('claude-haiku-4[-.]5', 1.00::numeric, 5.00::numeric, 0.10::numeric, 1.25::numeric),
            ('deepseek-v4-pro', 1.74::numeric, 3.48::numeric, 0.0145::numeric, 1.74::numeric),
            ('deepseek-v4-flash|^deepseek-chat$|^deepseek-reasoner$', 0.56::numeric, 1.12::numeric, 0.0112::numeric, 0.56::numeric),
            ('minimax-m2[.]7.*highspeed|highspeed.*minimax-m2[.]7', 0.60::numeric, 2.40::numeric, 0.06::numeric, 0.375::numeric),
            ('minimax-m2[.]7', 0.30::numeric, 1.20::numeric, 0.06::numeric, 0.375::numeric),
            ('gemini-3-flash', 0.50::numeric, 3.00::numeric, 0.05::numeric, 0.50::numeric),
            ('gemini-3[.]1-pro', 2.00::numeric, 12.00::numeric, 0.20::numeric, 2.00::numeric),
            ('gemini-2[.]5-pro', 1.25::numeric, 10.00::numeric, 0.31::numeric, 1.25::numeric),
            ('gemini-2[.]5-flash', 0.30::numeric, 2.50::numeric, 0.03::numeric, 0.30::numeric),
            ('(^|/|:)grok-4\.5$', 2.00::numeric, 6.00::numeric, 0.30::numeric, 2.00::numeric),
            ('(^|/|:)grok-4\.3$', 1.25::numeric, 2.50::numeric, 0.20::numeric, 1.25::numeric),
            ('(^|/|:)grok-build-0\.1$', 1.00::numeric, 2.00::numeric, 0.20::numeric, 1.00::numeric),
            ('(^|/|:)grok-4\.20-multi-agent-0309$', 1.25::numeric, 2.50::numeric, 0.20::numeric, 1.25::numeric),
            ('(^|/|:)grok-4\.20-0309-reasoning$', 1.25::numeric, 2.50::numeric, 0.20::numeric, 1.25::numeric),
            ('(^|/|:)grok-4\.20-0309-non-reasoning$', 1.25::numeric, 2.50::numeric, 0.20::numeric, 1.25::numeric)
    ),
    candidates AS (
        SELECT
            tu.id,
            ROUND((tu.input_tokens * r.input_per_m
                + tu.output_tokens * r.output_per_m
                + tu.cache_read_tokens * r.cache_read_per_m
                + tu.cache_write_tokens * r.cache_write_per_m) * 10000)::bigint AS cost_usd_ticks
          FROM task_usage tu
          JOIN LATERAL (
              SELECT * FROM rates r
               WHERE LOWER(BTRIM(tu.model)) ~ r.pattern
               LIMIT 1
          ) r ON TRUE
         WHERE tu.cost_usd_ticks IS NULL
    ),
    marked_insert AS (
        INSERT INTO migration_225_task_usage_cost_backfill (task_usage_id)
        SELECT DISTINCT id FROM candidates WHERE cost_usd_ticks > 0
        RETURNING task_usage_id
    ),
    marked AS (
        SELECT c.id, c.cost_usd_ticks
          FROM candidates c
          JOIN (SELECT DISTINCT task_usage_id FROM migration_225_task_usage_cost_backfill) m ON m.task_usage_id = c.id
    )
    UPDATE task_usage tu
       SET cost_usd_ticks = m.cost_usd_ticks,
           updated_at = clock_timestamp()
      FROM marked m
     WHERE tu.id = m.id;

    PERFORM rollup_task_usage_hourly_window(v_started_at - interval '1 second', clock_timestamp() + interval '1 second');
END $$;

WITH seed(issue_identifier, repo_owner, repo_name, pr_number, title, state, html_url, branch, author_login, merged_at, closed_at, pr_created_at, pr_updated_at, head_sha, additions, deletions, changed_files) AS (
    VALUES
        ('ROL-28', 'newstead', 'multica', 2, 'ROL-28 add reasoning token usage plumbing', 'merged', 'https://github.com/newstead/multica/pull/2', 'agent/aq-go/rol-28-reasoning-tokens', 'app/multica-roller4', '2026-07-28T12:35:00Z'::timestamptz, '2026-07-28T12:35:00Z'::timestamptz, '2026-07-28T10:38:03Z'::timestamptz, '2026-07-28T12:35:01Z'::timestamptz, 'b7b9b8d41bbf0377e727208d0a3f7df1ffecd6f3', 390, 19, 17),
        ('ROL-29', 'newstead', 'multica', 3, 'ROL-29: capture reasoning tokens in provider parsers', 'merged', 'https://github.com/newstead/multica/pull/3', 'agent/aq-go/rol-29-reasoning-token-parsers', 'app/multica-roller4', '2026-07-28T11:51:15Z'::timestamptz, '2026-07-28T11:51:15Z'::timestamptz, '2026-07-28T10:53:01Z'::timestamptz, '2026-07-28T11:51:17Z'::timestamptz, 'bfff9fd4f5481457cb616efdce87cce0ffd7ca46', 501, 65, 29),
        ('ROL-30', 'newstead', 'multica', 1, 'ROL-30: capture daemon task diff stats', 'merged', 'https://github.com/newstead/multica/pull/1', 'agent/aq-go/57d40924', 'app/multica-roller4', '2026-07-28T10:56:55Z'::timestamptz, '2026-07-28T10:56:55Z'::timestamptz, '2026-07-28T10:15:41Z'::timestamptz, '2026-07-28T10:56:57Z'::timestamptz, 'a855430d6ddc70d2859b31233904a4ce2722ae2e', 484, 75, 7),
        ('ROL-31', 'newstead', 'multica-ops', 1, 'ROL-31: add quality dashboard service', 'merged', 'https://github.com/newstead/multica-ops/pull/1', 'agent/aq-py/rol-31-quality-dashboard', 'app/multica-roller4', '2026-07-28T14:34:39Z'::timestamptz, '2026-07-28T14:34:39Z'::timestamptz, '2026-07-28T12:52:40Z'::timestamptz, '2026-07-28T14:34:40Z'::timestamptz, '093763404474a3f7db09cf3a0a2b27678e4ef5ce', 376, 82, 4),
        ('ROL-32', 'newstead', 'multica-ops', 2, 'ROL-32: wire quality dashboard deploy', 'merged', 'https://github.com/newstead/multica-ops/pull/2', 'agent/aq-py/rol-32-quality-deploy-wiring', 'app/multica-roller4', '2026-07-28T14:07:11Z'::timestamptz, '2026-07-28T14:07:11Z'::timestamptz, '2026-07-28T13:03:08Z'::timestamptz, '2026-07-28T14:07:13Z'::timestamptz, '31af4e9d57aeab5e9da987c4d91f2e7eb17d4521', 1305, 7, 13),
        ('ROL-33', 'newstead', 'multica-ops', 4, 'ROL-33: fix tokens endpoint on older usage schema', 'merged', 'https://github.com/newstead/multica-ops/pull/4', 'agent/aq-py/b19e13fd', 'app/multica-roller4', '2026-07-28T15:47:30Z'::timestamptz, '2026-07-28T15:47:30Z'::timestamptz, '2026-07-28T15:31:02Z'::timestamptz, '2026-07-28T15:47:32Z'::timestamptz, 'f8aad2e437626f337962947eccbb7d24a78b790e', 34, 2, 2),
        ('ROL-33', 'newstead', 'multica-ops', 5, 'ROL-33: qualify token model query columns', 'merged', 'https://github.com/newstead/multica-ops/pull/5', 'agent/aq-py/rol-33-tokens-schema', 'app/multica-roller4', '2026-07-28T16:09:35Z'::timestamptz, '2026-07-28T16:09:35Z'::timestamptz, '2026-07-28T15:58:14Z'::timestamptz, '2026-07-28T16:09:37Z'::timestamptz, '0e207e5dab328a302d335261bb6cf23343c0a8f6', 6, 3, 2)
),
inserted AS (
    INSERT INTO github_pull_request (
        workspace_id, installation_id, repo_owner, repo_name, pr_number,
        title, state, html_url, branch, author_login,
        merged_at, closed_at, pr_created_at, pr_updated_at, head_sha,
        additions, deletions, changed_files
    )
    SELECT DISTINCT ON (i.workspace_id, s.repo_owner, s.repo_name, s.pr_number)
        i.workspace_id, 0, s.repo_owner, s.repo_name, s.pr_number,
        s.title, s.state, s.html_url, s.branch, s.author_login,
        s.merged_at, s.closed_at, s.pr_created_at, s.pr_updated_at, s.head_sha,
        s.additions, s.deletions, s.changed_files
      FROM seed s
      JOIN issue i ON TRUE
      JOIN workspace w ON w.id = i.workspace_id
     WHERE CONCAT(w.issue_prefix, '-', i.number) = s.issue_identifier
     ORDER BY i.workspace_id, s.repo_owner, s.repo_name, s.pr_number
    ON CONFLICT (workspace_id, repo_owner, repo_name, pr_number) DO NOTHING
    RETURNING id
)
INSERT INTO migration_225_github_pr_backfill (pull_request_id)
SELECT id FROM inserted;

WITH seed(issue_identifier, repo_owner, repo_name, pr_number) AS (
    VALUES
        ('ROL-28', 'newstead', 'multica', 2),
        ('ROL-29', 'newstead', 'multica', 3),
        ('ROL-30', 'newstead', 'multica', 1),
        ('ROL-31', 'newstead', 'multica-ops', 1),
        ('ROL-32', 'newstead', 'multica-ops', 2),
        ('ROL-33', 'newstead', 'multica-ops', 4),
        ('ROL-33', 'newstead', 'multica-ops', 5)
),
links AS (
    SELECT i.id AS issue_id, pr.id AS pull_request_id
      FROM seed s
      JOIN issue i ON TRUE
      JOIN workspace w ON w.id = i.workspace_id
      JOIN github_pull_request pr
        ON pr.workspace_id = i.workspace_id
       AND pr.repo_owner = s.repo_owner
       AND pr.repo_name = s.repo_name
       AND pr.pr_number = s.pr_number
     WHERE CONCAT(w.issue_prefix, '-', i.number) = s.issue_identifier
),
inserted AS (
    INSERT INTO issue_pull_request (
        issue_id, pull_request_id, linked_by_type, linked_by_id,
        close_intent, reference_only
    )
    SELECT issue_id, pull_request_id, 'system', NULL, FALSE, FALSE
      FROM links
    ON CONFLICT (issue_id, pull_request_id) DO NOTHING
    RETURNING issue_id, pull_request_id
)
INSERT INTO migration_225_issue_pr_backfill (issue_id, pull_request_id)
SELECT issue_id, pull_request_id FROM inserted;
