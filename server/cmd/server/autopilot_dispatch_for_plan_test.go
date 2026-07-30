package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestDispatchAutopilotForPlanIsIdempotent locks in the
// occurrence-level idempotency contract (MUL-3551):
//
//   - A second DispatchAutopilotForPlan with the same (trigger_id,
//     planned_at) MUST return the SAME run row that the first call
//     created. No second autopilot_run, no second issue / task, no
//     second failure recorded.
//
// This is the dispatch-layer half of the two-defence design. The
// primary defence lives in sys_cron_executions
// (uq_sys_cron_execution). This one catches the stale-steal case
// where a runner crashes between "create run" and "write SUCCESS in
// sys_cron_executions": the next runner re-enters the dispatch and
// must reuse the in-flight run instead of duplicating it.
func TestDispatchAutopilotForPlanIsIdempotent(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)
	registerAutopilotListeners(bus, autopilotSvc)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Dispatch for plan idempotency",
		Description:        pgtype.Text{String: "Dispatch for plan test", Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "run_only",
		IssueTitleTemplate: pgtype.Text{},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(),
			`DELETE FROM autopilot WHERE id = $1`, ap.ID); err != nil {
			t.Logf("cleanup autopilot: %v", err)
		}
	})

	trigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
		AutopilotID:    ap.ID,
		Kind:           "schedule",
		Enabled:        true,
		CronExpression: pgtype.Text{String: "*/5 * * * *", Valid: true},
		Timezone:       pgtype.Text{String: "UTC", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAutopilotTrigger: %v", err)
	}

	// Use a fixed planned_at so the partial unique index has something
	// concrete to enforce against. Truncate to seconds — the column is
	// TIMESTAMPTZ and pgx round-trips sub-microsecond, but we want the
	// comparison to be byte-stable across the two calls.
	plannedAt := time.Now().UTC().Truncate(time.Second).Add(-30 * time.Second)

	first, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, trigger.ID, "schedule", nil, plannedAt,
	)
	if err != nil {
		t.Fatalf("first DispatchAutopilotForPlan: %v", err)
	}
	if first == nil {
		t.Fatalf("first call returned nil run")
	}
	if !first.PlannedAt.Valid {
		t.Fatalf("first run should have planned_at set")
	}
	if !first.PlannedAt.Time.Equal(plannedAt) {
		t.Fatalf("first run planned_at mismatch: got %s, want %s",
			first.PlannedAt.Time.Format(time.RFC3339Nano),
			plannedAt.Format(time.RFC3339Nano))
	}

	// Second call with the SAME (trigger, planned_at) must reuse the
	// first run, not create a new one.
	second, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, trigger.ID, "schedule", nil, plannedAt,
	)
	if err != nil {
		t.Fatalf("second DispatchAutopilotForPlan: %v", err)
	}
	if second == nil {
		t.Fatalf("second call returned nil run")
	}
	if second.ID != first.ID {
		t.Fatalf("second call must reuse first run: first.ID=%s second.ID=%s",
			util.UUIDToString(first.ID), util.UUIDToString(second.ID))
	}

	// Belt-and-suspenders: the partial unique index plus the lookup
	// in DispatchAutopilotForPlan together guarantee exactly one row.
	var rowCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM autopilot_run WHERE autopilot_id = $1`, ap.ID,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("expected exactly 1 autopilot_run for the (trigger, planned_at) pair, got %d", rowCount)
	}

	// A different planned_at for the SAME trigger while the first scheduled
	// run is still active is an overlap: record an auditable skipped run, but
	// do not enqueue a second agent task.
	plannedAt2 := plannedAt.Add(5 * time.Minute)
	third, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, trigger.ID, "schedule", nil, plannedAt2,
	)
	if err != nil {
		t.Fatalf("third DispatchAutopilotForPlan with overlapping planned_at: %v", err)
	}
	if third == nil || third.ID == first.ID {
		t.Fatalf("overlap must produce a distinct skipped audit run, got %+v", third)
	}
	if third.Status != "skipped" {
		t.Fatalf("overlap run status = %q, want skipped", third.Status)
	}
	if !third.FailureReason.Valid || third.FailureReason.String != "skipped_overlap" {
		t.Fatalf("overlap failure_reason = %+v, want skipped_overlap", third.FailureReason)
	}
	if third.TaskID.Valid || third.IssueID.Valid {
		t.Fatalf("overlap run must not create downstream work, got task_id=%s issue_id=%s",
			util.UUIDToString(third.TaskID), util.UUIDToString(third.IssueID))
	}

	var taskCount int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_task_queue WHERE autopilot_run_id IN ($1, $2)`, first.ID, third.ID,
	).Scan(&taskCount); err != nil {
		t.Fatalf("count tasks for first+overlap: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("expected only the admitted scheduled run to enqueue a task, got %d tasks", taskCount)
	}

	// Manual runs remain independent of scheduled-overlap admission.
	manual, err := autopilotSvc.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "manual", nil)
	if err != nil {
		t.Fatalf("manual DispatchAutopilot during active schedule: %v", err)
	}
	if manual == nil || manual.Status != "running" || !manual.TaskID.Valid {
		t.Fatalf("manual dispatch during active schedule = %+v, want running with task_id", manual)
	}

	// Distinct schedule triggers on the same autopilot remain independent.
	otherTrigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
		AutopilotID:    ap.ID,
		Kind:           "schedule",
		Enabled:        true,
		CronExpression: pgtype.Text{String: "*/5 * * * *", Valid: true},
		Timezone:       pgtype.Text{String: "UTC", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAutopilotTrigger other: %v", err)
	}
	otherRun, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, otherTrigger.ID, "schedule", nil, plannedAt2,
	)
	if err != nil {
		t.Fatalf("DispatchAutopilotForPlan for distinct trigger: %v", err)
	}
	if otherRun == nil || otherRun.Status != "running" || !otherRun.TaskID.Valid {
		t.Fatalf("distinct trigger dispatch = %+v, want running with task_id", otherRun)
	}

	// Completing the admitted run releases the same schedule trigger for the next tick.
	if _, err := queries.UpdateAutopilotRunCompleted(ctx, db.UpdateAutopilotRunCompletedParams{ID: first.ID}); err != nil {
		t.Fatalf("complete first run: %v", err)
	}
	plannedAt3 := plannedAt.Add(10 * time.Minute)
	fourth, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, trigger.ID, "schedule", nil, plannedAt3,
	)
	if err != nil {
		t.Fatalf("DispatchAutopilotForPlan after completion: %v", err)
	}
	if fourth == nil || fourth.Status != "running" || !fourth.TaskID.Valid {
		t.Fatalf("post-completion dispatch = %+v, want running with task_id", fourth)
	}

	// Cancelling an admitted run_only task also terminalizes the run through the
	// task listener, releasing the same schedule trigger for a later tick.
	if _, err := taskSvc.CancelTask(ctx, fourth.TaskID); err != nil {
		t.Fatalf("cancel fourth task: %v", err)
	}
	cancelledRun, err := queries.GetAutopilotRun(ctx, fourth.ID)
	if err != nil {
		t.Fatalf("load cancelled run: %v", err)
	}
	if cancelledRun.Status != "failed" {
		t.Fatalf("cancelled task should mark run failed, got %q", cancelledRun.Status)
	}
	plannedAt4 := plannedAt.Add(15 * time.Minute)
	fifth, err := autopilotSvc.DispatchAutopilotForPlan(
		ctx, ap, trigger.ID, "schedule", nil, plannedAt4,
	)
	if err != nil {
		t.Fatalf("DispatchAutopilotForPlan after cancellation: %v", err)
	}
	if fifth == nil || fifth.Status != "running" || !fifth.TaskID.Valid {
		t.Fatalf("post-cancellation dispatch = %+v, want running with task_id", fifth)
	}
}

func TestDispatchAutopilotForPlanCoalescesConcurrentOverlappingRuns(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)

	newSvc := func() *service.AutopilotService {
		q := db.New(testPool)
		bus := events.New()
		taskSvc := service.NewTaskService(q, testPool, nil, bus)
		return service.NewAutopilotService(q, testPool, bus, taskSvc)
	}

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Concurrent scheduled overlap",
		Description:        pgtype.Text{String: "overlap race test", Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "run_only",
		IssueTitleTemplate: pgtype.Text{},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(),
			`DELETE FROM autopilot WHERE id = $1`, ap.ID); err != nil {
			t.Logf("cleanup autopilot: %v", err)
		}
	})

	trigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
		AutopilotID:    ap.ID,
		Kind:           "schedule",
		Enabled:        true,
		CronExpression: pgtype.Text{String: "*/15 * * * *", Valid: true},
		Timezone:       pgtype.Text{String: "UTC", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAutopilotTrigger: %v", err)
	}

	plannedAt := time.Now().UTC().Truncate(time.Second).Add(-15 * time.Minute)
	barrierKey := time.Now().UnixNano()
	barrierConn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire barrier connection: %v", err)
	}
	defer barrierConn.Release()
	if _, err := barrierConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, barrierKey); err != nil {
		t.Fatalf("take insert barrier lock: %v", err)
	}
	barrierLocked := true
	unlockBarrier := func() {
		if barrierLocked {
			_, _ = barrierConn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, barrierKey)
			barrierLocked = false
		}
	}
	defer unlockBarrier()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	seqName := "autopilot_overlap_barrier_seq_" + suffix
	fnName := "autopilot_overlap_barrier_fn_" + suffix
	triggerName := "autopilot_overlap_barrier_tr_" + suffix
	if _, err := testPool.Exec(ctx, fmt.Sprintf(`
		CREATE SEQUENCE %s;
		CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.autopilot_id = '%s'::uuid
			   AND NEW.trigger_id = '%s'::uuid
			   AND NEW.source = 'schedule'
			   AND NEW.status IN ('issue_created', 'running') THEN
				PERFORM nextval('%s'::regclass);
				PERFORM pg_advisory_lock(%d);
				PERFORM pg_advisory_unlock(%d);
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER %s
		BEFORE INSERT ON autopilot_run
		FOR EACH ROW EXECUTE FUNCTION %s();
	`, seqName, fnName, util.UUIDToString(ap.ID), util.UUIDToString(trigger.ID), seqName, barrierKey, barrierKey, triggerName, fnName)); err != nil {
		t.Fatalf("install insert barrier trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), fmt.Sprintf(`
			DROP TRIGGER IF EXISTS %s ON autopilot_run;
			DROP FUNCTION IF EXISTS %s();
			DROP SEQUENCE IF EXISTS %s;
		`, triggerName, fnName, seqName))
	})

	services := []*service.AutopilotService{newSvc(), newSvc()}
	plans := []time.Time{plannedAt, plannedAt.Add(15 * time.Minute)}
	runs := make([]*db.AutopilotRun, len(services))
	errs := make([]error, len(services))

	var ready sync.WaitGroup
	var start sync.WaitGroup
	ready.Add(len(services))
	start.Add(1)
	var done sync.WaitGroup
	done.Add(len(services))
	for i := range services {
		i := i
		go func() {
			defer done.Done()
			ready.Done()
			start.Wait()
			runs[i], errs[i] = services[i].DispatchAutopilotForPlan(
				ctx, ap, trigger.ID, "schedule", nil, plans[i],
			)
		}()
	}
	ready.Wait()
	start.Done()
	if err := waitForBarrierCount(ctx, seqName, len(services)); err != nil {
		unlockBarrier()
		done.Wait()
		t.Fatalf("wait for both dispatchers at insert barrier: %v", err)
	}
	unlockBarrier()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("DispatchAutopilotForPlan[%d]: %v", i, err)
		}
		if runs[i] == nil {
			t.Fatalf("DispatchAutopilotForPlan[%d] returned nil run", i)
		}
	}

	var running, skipped, taskRows int
	if err := testPool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status = 'running'),
			COUNT(*) FILTER (WHERE status = 'skipped' AND failure_reason = 'skipped_overlap'),
			(SELECT COUNT(*) FROM agent_task_queue WHERE autopilot_run_id IN (SELECT id FROM autopilot_run WHERE trigger_id = $1))
		FROM autopilot_run
		WHERE trigger_id = $1
	`, trigger.ID).Scan(&running, &skipped, &taskRows); err != nil {
		t.Fatalf("count overlap results: %v", err)
	}
	if running != 1 || skipped != 1 {
		t.Fatalf("overlap race should produce 1 running + 1 skipped_overlap run, got running=%d skipped=%d", running, skipped)
	}
	if taskRows != 1 {
		t.Fatalf("overlap race should enqueue exactly 1 task, got %d", taskRows)
	}
}

func waitForBarrierCount(ctx context.Context, seqName string, want int) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var got int
		if err := testPool.QueryRow(ctx, fmt.Sprintf(`SELECT CASE WHEN is_called THEN last_value ELSE 0 END FROM %s`, seqName)).Scan(&got); err != nil {
			return err
		}
		if got >= want {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("barrier count = %d, want >= %d", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDispatchAutopilotForPlanSkipsRepeatedTicksWhileCapacityOccupied(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, runtime_id::text
		FROM agent
		WHERE workspace_id = $1 AND runtime_id IS NOT NULL
		ORDER BY created_at ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("load fixture agent/runtime: %v", err)
	}

	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Saturated capacity scheduled overlap",
		Description:        pgtype.Text{String: "saturated capacity regression", Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "run_only",
		IssueTitleTemplate: pgtype.Text{},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM autopilot WHERE id = $1`, ap.ID); err != nil {
			t.Logf("cleanup autopilot: %v", err)
		}
	})

	trigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
		AutopilotID:    ap.ID,
		Kind:           "schedule",
		Enabled:        true,
		CronExpression: pgtype.Text{String: "*/15 * * * *", Valid: true},
		Timezone:       pgtype.Text{String: "UTC", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateAutopilotTrigger: %v", err)
	}

	capacityMarker := "saturated capacity regression " + time.Now().UTC().Format("20060102150405.000000000")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, started_at, trigger_summary)
		SELECT $1::uuid, $2::uuid, 'running', 0, now(), $3
		FROM generate_series(1, 4)
	`, agentID, runtimeID, capacityMarker); err != nil {
		t.Fatalf("seed occupied capacity tasks: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE trigger_summary = $1`, capacityMarker); err != nil {
			t.Logf("cleanup occupied capacity tasks: %v", err)
		}
	})

	plannedAt := time.Now().UTC().Truncate(time.Second).Add(-45 * time.Minute)
	for i := 0; i < 4; i++ {
		run, err := autopilotSvc.DispatchAutopilotForPlan(
			ctx, ap, trigger.ID, "schedule", nil, plannedAt.Add(time.Duration(i)*15*time.Minute),
		)
		if err != nil {
			t.Fatalf("DispatchAutopilotForPlan tick %d: %v", i, err)
		}
		if run == nil {
			t.Fatalf("DispatchAutopilotForPlan tick %d returned nil", i)
		}
	}

	var activeRuns, skippedRuns, taskRows int
	if err := testPool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('issue_created', 'running')),
			COUNT(*) FILTER (WHERE status = 'skipped' AND failure_reason = 'skipped_overlap'),
			(SELECT COUNT(*) FROM agent_task_queue WHERE autopilot_run_id IN (SELECT id FROM autopilot_run WHERE trigger_id = $1))
		FROM autopilot_run
		WHERE trigger_id = $1
	`, trigger.ID).Scan(&activeRuns, &skippedRuns, &taskRows); err != nil {
		t.Fatalf("count saturated overlap results: %v", err)
	}
	if activeRuns != 1 || skippedRuns != 3 {
		t.Fatalf("saturated repeated ticks active/skipped = %d/%d, want 1/3", activeRuns, skippedRuns)
	}
	if taskRows != 1 {
		t.Fatalf("saturated repeated ticks should enqueue exactly 1 autopilot task, got %d", taskRows)
	}
}

func TestDispatchAutopilotSuppressesRecentDuplicateIssue(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

	var agentID string
	if err := testPool.QueryRow(ctx,
		`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
		testWorkspaceID,
	).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	title := "Autopilot recent duplicate issue " + time.Now().UTC().Format("20060102150405.000000000")
	ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
		WorkspaceID:        parseUUID(testWorkspaceID),
		Title:              "Recent duplicate issue guard",
		Description:        pgtype.Text{String: "Recent duplicate issue guard test", Valid: true},
		AssigneeType:       "agent",
		AssigneeID:         parseUUID(agentID),
		Status:             "active",
		ExecutionMode:      "create_issue",
		IssueTitleTemplate: pgtype.Text{String: title, Valid: true},
		CreatedByType:      "member",
		CreatedByID:        parseUUID(testUserID),
	})
	if err != nil {
		t.Fatalf("CreateAutopilot: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = testPool.Exec(bg, `DELETE FROM autopilot WHERE id = $1`, ap.ID)
		_, _ = testPool.Exec(bg, `DELETE FROM issue WHERE workspace_id = $1 AND title = $2`, testWorkspaceID, title)
	})

	first, err := autopilotSvc.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "manual", nil)
	if err != nil {
		t.Fatalf("first DispatchAutopilot: %v", err)
	}
	if first == nil || first.Status != "issue_created" || !first.IssueID.Valid {
		t.Fatalf("first dispatch = %+v, want issue_created with issue_id", first)
	}

	second, err := autopilotSvc.DispatchAutopilot(ctx, ap, pgtype.UUID{}, "manual", nil)
	if err != nil {
		t.Fatalf("second DispatchAutopilot: %v", err)
	}
	if second == nil || second.Status != "skipped" {
		t.Fatalf("second dispatch = %+v, want skipped duplicate run", second)
	}
	if second.IssueID.Valid {
		t.Fatalf("duplicate run linked issue_id=%s, want no new issue", util.UUIDToString(second.IssueID))
	}

	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM issue WHERE workspace_id = $1 AND title = $2`,
		testWorkspaceID, title,
	).Scan(&count); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if count != 1 {
		t.Fatalf("recent duplicate autopilot dispatch should leave 1 matching issue, got %d", count)
	}
}

// TestDispatchAutopilotForPlanRejectsZeroArgs locks in the
// fail-loud contract: a caller that forgets to set trigger_id or
// planned_at would silently disable the idempotency guard, and the
// only safe answer is an error.
func TestDispatchAutopilotForPlanRejectsZeroArgs(t *testing.T) {
	ctx := context.Background()
	queries := db.New(testPool)
	bus := events.New()
	taskSvc := service.NewTaskService(queries, testPool, nil, bus)
	autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

	ap := db.Autopilot{
		ID:            parseUUID(testWorkspaceID), // placeholder; will not be loaded since validation fails first
		WorkspaceID:   parseUUID(testWorkspaceID),
		ExecutionMode: "run_only",
		AssigneeType:  "agent",
		AssigneeID:    parseUUID(testWorkspaceID), // arbitrary; we never get past the input guard
		Status:        "active",
	}

	t.Run("invalid trigger_id", func(t *testing.T) {
		_, err := autopilotSvc.DispatchAutopilotForPlan(
			ctx, ap, pgtype.UUID{}, "schedule", nil, time.Now().UTC(),
		)
		if err == nil {
			t.Fatalf("expected error for invalid trigger_id")
		}
	})

	t.Run("zero planned_at", func(t *testing.T) {
		_, err := autopilotSvc.DispatchAutopilotForPlan(
			ctx, ap, parseUUID(testWorkspaceID), "schedule", nil, time.Time{},
		)
		if err == nil {
			t.Fatalf("expected error for zero planned_at")
		}
	})
}

// TestDispatchAutopilotForPlanRecoversPartialRun is the regression
// for the #4443 review blocker:
//
//	"DispatchAutopilotForPlan reuses existing run unconditionally,
//	 will mark a half-written run as SUCCESS even when no
//	 issue/task was ever created."
//
// We seed a partial-state autopilot_run for (trigger, planned_at) —
// the run exists with a non-terminal status but the corresponding
// downstream linkage (task_id for run_only, issue_id for create_issue)
// is NULL. A subsequent DispatchAutopilotForPlan call at the same
// (trigger, planned_at) MUST NOT return the partial row as-is;
// instead it must mark the partial row FAILED + clear its planned_at
// to release the partial-unique slot, then create a fresh dispatched
// run with the downstream linkage actually populated.
func TestDispatchAutopilotForPlanRecoversPartialRun(t *testing.T) {
	for _, mode := range []string{"run_only", "create_issue"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			queries := db.New(testPool)
			bus := events.New()
			taskSvc := service.NewTaskService(queries, testPool, nil, bus)
			autopilotSvc := service.NewAutopilotService(queries, testPool, bus, taskSvc)

			var agentID string
			if err := testPool.QueryRow(ctx,
				`SELECT id::text FROM agent WHERE workspace_id = $1 ORDER BY created_at ASC LIMIT 1`,
				testWorkspaceID,
			).Scan(&agentID); err != nil {
				t.Fatalf("load fixture agent: %v", err)
			}

			ap, err := queries.CreateAutopilot(ctx, db.CreateAutopilotParams{
				WorkspaceID:        parseUUID(testWorkspaceID),
				Title:              "Partial recovery " + mode,
				Description:        pgtype.Text{String: "partial run recovery test", Valid: true},
				AssigneeType:       "agent",
				AssigneeID:         parseUUID(agentID),
				Status:             "active",
				ExecutionMode:      mode,
				IssueTitleTemplate: pgtype.Text{String: "Partial recovery", Valid: true},
				CreatedByType:      "member",
				CreatedByID:        parseUUID(testUserID),
			})
			if err != nil {
				t.Fatalf("CreateAutopilot: %v", err)
			}
			t.Cleanup(func() {
				if _, err := testPool.Exec(context.Background(),
					`DELETE FROM autopilot WHERE id = $1`, ap.ID); err != nil {
					t.Logf("cleanup autopilot: %v", err)
				}
			})

			trigger, err := queries.CreateAutopilotTrigger(ctx, db.CreateAutopilotTriggerParams{
				AutopilotID:    ap.ID,
				Kind:           "schedule",
				Enabled:        true,
				CronExpression: pgtype.Text{String: "*/5 * * * *", Valid: true},
				Timezone:       pgtype.Text{String: "UTC", Valid: true},
			})
			if err != nil {
				t.Fatalf("CreateAutopilotTrigger: %v", err)
			}

			plannedAt := time.Now().UTC().Truncate(time.Second).Add(-2 * time.Minute)
			plannedTS := pgtype.Timestamptz{Time: plannedAt, Valid: true}

			// Seed a PARTIAL run: a prior attempt wrote the run row
			// (status reflects the dispatch path's initial state) but
			// crashed before creating the downstream resource —
			// task_id is NULL for run_only, issue_id is NULL for
			// create_issue.
			initialStatus := "running"
			if mode == "create_issue" {
				initialStatus = "issue_created"
			}
			partial, err := queries.CreateAutopilotRun(ctx, db.CreateAutopilotRunParams{
				AutopilotID:    ap.ID,
				TriggerID:      trigger.ID,
				Source:         "schedule",
				Status:         initialStatus,
				TriggerPayload: nil,
				PlannedAt:      plannedTS,
			})
			if err != nil {
				t.Fatalf("seed partial run: %v", err)
			}
			// Confirm the partial state: no downstream linkage.
			if partial.TaskID.Valid {
				t.Fatalf("seed partial run should have task_id=NULL, got %s", util.UUIDToString(partial.TaskID))
			}
			if partial.IssueID.Valid {
				t.Fatalf("seed partial run should have issue_id=NULL, got %s", util.UUIDToString(partial.IssueID))
			}

			// Retry the dispatch — this is the stale-steal codepath.
			fresh, err := autopilotSvc.DispatchAutopilotForPlan(
				ctx, ap, trigger.ID, "schedule", nil, plannedAt,
			)
			if err != nil {
				t.Fatalf("DispatchAutopilotForPlan retry: %v", err)
			}
			if fresh == nil {
				t.Fatalf("retry returned nil run")
			}
			if fresh.ID == partial.ID {
				t.Fatalf("retry must NOT reuse the partial run; got the same id %s", util.UUIDToString(fresh.ID))
			}

			// The partial row must now be FAILED with planned_at
			// cleared, so the new row's planned_at is unique.
			var partialStatus string
			var partialPlannedAt pgtype.Timestamptz
			var partialFailureReason pgtype.Text
			if err := testPool.QueryRow(ctx,
				`SELECT status, planned_at, failure_reason FROM autopilot_run WHERE id = $1`,
				partial.ID,
			).Scan(&partialStatus, &partialPlannedAt, &partialFailureReason); err != nil {
				t.Fatalf("read partial row after recovery: %v", err)
			}
			if partialStatus != "failed" {
				t.Fatalf("partial run must be marked failed, got %q", partialStatus)
			}
			if partialPlannedAt.Valid {
				t.Fatalf("partial run planned_at must be cleared to release partial-unique slot, still valid")
			}
			if !partialFailureReason.Valid || partialFailureReason.String == "" {
				t.Fatalf("partial run must carry a recovery failure_reason for ops, got empty")
			}

			// The fresh row must carry the original planned_at and a
			// real downstream linkage from the just-completed
			// dispatch.
			if !fresh.PlannedAt.Valid {
				t.Fatalf("fresh run planned_at must be set")
			}
			if !fresh.PlannedAt.Time.Equal(plannedAt) {
				t.Fatalf("fresh run planned_at mismatch: got %s want %s",
					fresh.PlannedAt.Time.Format(time.RFC3339Nano),
					plannedAt.Format(time.RFC3339Nano))
			}
			switch mode {
			case "run_only":
				if !fresh.TaskID.Valid {
					t.Fatalf("run_only retry must produce a run with task_id set")
				}
			case "create_issue":
				if !fresh.IssueID.Valid {
					t.Fatalf("create_issue retry must produce a run with issue_id set")
				}
			}

			// Verify the partial-unique constraint is happy: exactly
			// one row per (trigger_id, planned_at) where both are
			// non-NULL.
			var liveRows int
			if err := testPool.QueryRow(ctx, `
				SELECT COUNT(*) FROM autopilot_run
				 WHERE trigger_id = $1 AND planned_at = $2
			`, trigger.ID, plannedTS).Scan(&liveRows); err != nil {
				t.Fatalf("count live (trigger, planned) rows: %v", err)
			}
			if liveRows != 1 {
				t.Fatalf("expected exactly 1 live row at (trigger, planned_at) after recovery, got %d", liveRows)
			}
		})
	}
}
