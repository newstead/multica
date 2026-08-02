-- Backfill server-estimated task_usage costs for rows whose daemon did not
-- report an authoritative provider cost, using the same static model pricing
-- table as server/internal/metrics/pricing.go at the time this migration was
-- authored. Reasoning tokens are not priced separately; providers include them
-- in output-side billing.
WITH alias_rules(ord, provider, canonical_model, input_per_m, cache_read_per_m, cache_write_per_m, output_per_m, pattern) AS (
    VALUES
        (1, 'openai', 'gpt-5.6-sol', 5.00::numeric, 0.50::numeric, 6.25::numeric, 30.00::numeric, '(^|/|:)gpt-5\.6-sol$'),
        (2, 'openai', 'gpt-5.6-terra', 2.50::numeric, 0.25::numeric, 3.125::numeric, 15.00::numeric, '(^|/|:)gpt-5\.6-terra$'),
        (3, 'openai', 'gpt-5.6-luna', 1.00::numeric, 0.10::numeric, 1.25::numeric, 6.00::numeric, '(^|/|:)gpt-5\.6-luna$'),
        (4, 'openai', 'gpt-5.5', 5.00::numeric, 0.50::numeric, 0.50::numeric, 30.00::numeric, '(^|/|:)gpt-5[.-]5$|^gpt-5-5$'),
        (5, 'openai', 'gpt-5.4', 2.50::numeric, 0.25::numeric, 0.25::numeric, 15.00::numeric, '(^|/|:)gpt-5[.-]4($|-2026-03-05|-xhigh)'),
        (6, 'openai', 'gpt-5.4-mini', 0.75::numeric, 0.075::numeric, 0.075::numeric, 4.50::numeric, '(^|/|:)gpt-5[.-]4-mini($|[^a-z0-9])'),
        (7, 'openai', 'gpt-5.3-codex', 1.75::numeric, 0.175::numeric, 0.175::numeric, 14.00::numeric, '(^|/|:)gpt-5[.-]3-codex$'),
        (8, 'openai', 'gpt-5.2-codex', 1.75::numeric, 0.175::numeric, 0.175::numeric, 14.00::numeric, '(^|/|:)gpt-5[.-]2-codex$'),
        (9, 'anthropic', 'claude-sonnet-5', 2.00::numeric, 0.20::numeric, 2.50::numeric, 10.00::numeric, 'claude-sonnet-5|claude-5-sonnet'),
        (10, 'anthropic', 'claude-fable-5', 10.00::numeric, 1.00::numeric, 12.50::numeric, 50.00::numeric, 'claude-fable-5'),
        (11, 'anthropic', 'claude-opus-5', 5.00::numeric, 0.50::numeric, 6.25::numeric, 25.00::numeric, 'claude-opus-5'),
        (12, 'anthropic', 'claude-opus-4.8', 5.00::numeric, 0.50::numeric, 6.25::numeric, 25.00::numeric, 'claude-opus-4[-.]8'),
        (13, 'anthropic', 'claude-opus-4.7', 5.00::numeric, 0.50::numeric, 6.25::numeric, 25.00::numeric, 'claude-opus-4[-.]7'),
        (14, 'anthropic', 'claude-opus-4.6', 5.00::numeric, 0.50::numeric, 6.25::numeric, 25.00::numeric, 'claude-opus-4[-.]6'),
        (15, 'anthropic', 'claude-opus-4.5', 5.00::numeric, 0.50::numeric, 6.25::numeric, 25.00::numeric, 'claude-opus-4[-.]5'),
        (16, 'anthropic', 'claude-sonnet-4.6', 3.00::numeric, 0.30::numeric, 3.75::numeric, 15.00::numeric, 'claude-sonnet-4[-.]6|claude-4[-.]6-sonnet'),
        (17, 'anthropic', 'claude-sonnet-4.5', 3.00::numeric, 0.30::numeric, 3.75::numeric, 15.00::numeric, 'claude-sonnet-4[-.]5|claude-4[-.]5-sonnet'),
        (18, 'anthropic', 'claude-haiku-4.5', 1.00::numeric, 0.10::numeric, 1.25::numeric, 5.00::numeric, 'claude-haiku-4[-.]5'),
        (19, 'deepseek', 'v4-pro', 1.74::numeric, 0.0145::numeric, 1.74::numeric, 3.48::numeric, 'deepseek-v4-pro'),
        (20, 'deepseek', 'v4-flash', 0.56::numeric, 0.0112::numeric, 0.56::numeric, 1.12::numeric, 'deepseek-v4-flash|^deepseek-chat$|^deepseek-reasoner$'),
        (21, 'minimax', 'm2.7-highspeed', 0.60::numeric, 0.06::numeric, 0.375::numeric, 2.40::numeric, 'minimax-m2[.]7.*highspeed|highspeed.*minimax-m2[.]7'),
        (22, 'minimax', 'm2.7', 0.30::numeric, 0.06::numeric, 0.375::numeric, 1.20::numeric, 'minimax-m2[.]7'),
        (23, 'google', 'gemini-3-flash', 0.50::numeric, 0.05::numeric, 0.50::numeric, 3.00::numeric, 'gemini-3-flash'),
        (24, 'google', 'gemini-3.1-pro', 2.00::numeric, 0.20::numeric, 2.00::numeric, 12.00::numeric, 'gemini-3[.]1-pro'),
        (25, 'google', 'gemini-2.5-pro', 1.25::numeric, 0.31::numeric, 1.25::numeric, 10.00::numeric, 'gemini-2[.]5-pro'),
        (26, 'google', 'gemini-2.5-flash', 0.30::numeric, 0.03::numeric, 0.30::numeric, 2.50::numeric, 'gemini-2[.]5-flash'),
        (27, 'xai', 'grok-4.5', 2.00::numeric, 0.30::numeric, 2.00::numeric, 6.00::numeric, '(^|/|:)grok-4\.5$'),
        (28, 'xai', 'grok-4.3', 1.25::numeric, 0.20::numeric, 1.25::numeric, 2.50::numeric, '(^|/|:)grok-4\.3$'),
        (29, 'xai', 'grok-build-0.1', 1.00::numeric, 0.20::numeric, 1.00::numeric, 2.00::numeric, '(^|/|:)grok-build-0\.1$'),
        (30, 'xai', 'grok-4.20-multi-agent-0309', 1.25::numeric, 0.20::numeric, 1.25::numeric, 2.50::numeric, '(^|/|:)grok-4\.20-multi-agent-0309$'),
        (31, 'xai', 'grok-4.20-0309-reasoning', 1.25::numeric, 0.20::numeric, 1.25::numeric, 2.50::numeric, '(^|/|:)grok-4\.20-0309-reasoning$'),
        (32, 'xai', 'grok-4.20-0309-non-reasoning', 1.25::numeric, 0.20::numeric, 1.25::numeric, 2.50::numeric, '(^|/|:)grok-4\.20-0309-non-reasoning$')
), priced_rows AS (
    SELECT
        tu.id,
        floor(((tu.input_tokens::numeric       * p.input_per_m) +
               (tu.output_tokens::numeric      * p.output_per_m) +
               (tu.cache_read_tokens::numeric  * p.cache_read_per_m) +
               (tu.cache_write_tokens::numeric * p.cache_write_per_m))
              / 1000000 * 10000000000)::bigint AS estimated_cost_usd_ticks
      FROM task_usage tu
      JOIN LATERAL (
          SELECT input_per_m, output_per_m, cache_read_per_m, cache_write_per_m
            FROM alias_rules
           WHERE lower(btrim(tu.model)) ~ pattern
           ORDER BY ord
           LIMIT 1
      ) p ON true
     WHERE tu.cost_usd_ticks IS NULL
), updated AS (
    UPDATE task_usage tu
       SET cost_usd_ticks = priced_rows.estimated_cost_usd_ticks,
           updated_at = now()
      FROM priced_rows
     WHERE tu.id = priced_rows.id
     RETURNING tu.created_at, tu.task_id, tu.provider, tu.model
)
INSERT INTO task_usage_hourly_dirty (
    bucket_hour, workspace_id, runtime_id, agent_id,
    project_id, provider, model
)
SELECT DISTINCT
    task_usage_hour_bucket(u.created_at),
    a.workspace_id,
    atq.runtime_id,
    atq.agent_id,
    i.project_id,
    u.provider,
    u.model
  FROM updated u
  JOIN agent_task_queue atq ON atq.id = u.task_id
  JOIN agent a ON a.id = atq.agent_id
  LEFT JOIN issue i ON i.id = atq.issue_id
 WHERE atq.runtime_id IS NOT NULL
ON CONFLICT ON CONSTRAINT uq_task_usage_hourly_dirty_key DO UPDATE
    SET enqueued_at = GREATEST(task_usage_hourly_dirty.enqueued_at, EXCLUDED.enqueued_at);

-- Refresh the affected historical buckets immediately so dashboard windows read
-- the same persisted cost as fresh task_usage rows after this migration.
SELECT rollup_task_usage_hourly_window('-infinity'::timestamptz, 'infinity'::timestamptz);
