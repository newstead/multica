CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_autopilot_run_schedule_active
    ON autopilot_run (autopilot_id, trigger_id)
    WHERE source = 'schedule'
      AND trigger_id IS NOT NULL
      AND status IN ('issue_created', 'running');
