package migrations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAutopilotScheduleOverlapMigrationsReconcileBeforeIndex(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect to Postgres: %v", err)
	}
	defer pool.Close()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire Postgres connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("autopilot_overlap_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
	if _, err := conn.Exec(ctx, `SET search_path TO `+schema); err != nil {
		t.Fatalf("set search_path: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		CREATE TABLE autopilot_run (
			id UUID PRIMARY KEY,
			autopilot_id UUID NOT NULL,
			trigger_id UUID NULL,
			source TEXT NOT NULL,
			status TEXT NOT NULL,
			issue_id UUID NULL,
			task_id UUID NULL,
			triggered_at TIMESTAMPTZ NOT NULL,
			completed_at TIMESTAMPTZ NULL,
			failure_reason TEXT NULL,
			planned_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			autopilot_run_id UUID NULL,
			status TEXT NOT NULL,
			completed_at TIMESTAMPTZ NULL,
			failure_reason TEXT NULL,
			error TEXT NULL
		);
	`); err != nil {
		t.Fatalf("create minimal tables: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO autopilot_run (
			id, autopilot_id, trigger_id, source, status, issue_id, task_id,
			triggered_at, planned_at, created_at
		) VALUES
			('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000011', 'schedule', 'running', NULL, '00000000-0000-0000-0000-000000000201', '2026-07-30 00:00:00+00', '2026-07-30 00:00:00+00', '2026-07-30 00:00:00+00'),
			('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000011', 'schedule', 'running', NULL, '00000000-0000-0000-0000-000000000202', '2026-07-30 00:15:00+00', '2026-07-30 00:15:00+00', '2026-07-30 00:15:00+00'),
			('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000011', 'schedule', 'issue_created', '00000000-0000-0000-0000-000000000301', NULL, '2026-07-30 00:30:00+00', '2026-07-30 00:30:00+00', '2026-07-30 00:30:00+00'),
			('00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000012', 'schedule', 'running', NULL, '00000000-0000-0000-0000-000000000204', '2026-07-30 00:00:00+00', '2026-07-30 00:00:00+00', '2026-07-30 00:00:00+00');
		INSERT INTO agent_task_queue (id, autopilot_run_id, status) VALUES
			('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000101', 'running'),
			('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000102', 'queued'),
			('00000000-0000-0000-0000-000000000204', '00000000-0000-0000-0000-000000000104', 'running');
	`); err != nil {
		t.Fatalf("seed duplicate active schedule runs: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "226_autopilot_schedule_overlap_reconcile.up.sql")

	var activeSameKey, skippedSameKey int
	if err := conn.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE status IN ('issue_created', 'running')),
			COUNT(*) FILTER (WHERE status = 'skipped' AND failure_reason = 'skipped_overlap')
		FROM autopilot_run
		WHERE autopilot_id = '00000000-0000-0000-0000-000000000001'
		  AND trigger_id = '00000000-0000-0000-0000-000000000011'
	`).Scan(&activeSameKey, &skippedSameKey); err != nil {
		t.Fatalf("count reconciled runs: %v", err)
	}
	if activeSameKey != 1 || skippedSameKey != 2 {
		t.Fatalf("reconcile active/skipped same key = %d/%d, want 1/2", activeSameKey, skippedSameKey)
	}

	var duplicateTaskStatus, duplicateTaskReason, duplicateTaskError string
	if err := conn.QueryRow(ctx, `
		SELECT status, COALESCE(failure_reason, ''), COALESCE(error, '')
		FROM agent_task_queue
		WHERE id = '00000000-0000-0000-0000-000000000202'
	`).Scan(&duplicateTaskStatus, &duplicateTaskReason, &duplicateTaskError); err != nil {
		t.Fatalf("read duplicate task: %v", err)
	}
	if duplicateTaskStatus != "cancelled" || duplicateTaskReason != "skipped_overlap" || duplicateTaskError != "skipped_overlap" {
		t.Fatalf("duplicate task = status %q reason %q error %q, want cancelled/skipped_overlap/skipped_overlap", duplicateTaskStatus, duplicateTaskReason, duplicateTaskError)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "227_autopilot_schedule_active_overlap_index.up.sql")

	var indexValid, indexUnique bool
	if err := conn.QueryRow(ctx, `
		SELECT i.indisvalid, i.indisunique
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'uq_autopilot_run_schedule_active'
	`, schema).Scan(&indexValid, &indexUnique); err != nil {
		t.Fatalf("read overlap index validity: %v", err)
	}
	if !indexValid || !indexUnique {
		t.Fatalf("overlap index valid/unique = %v/%v, want true/true", indexValid, indexUnique)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO autopilot_run (id, autopilot_id, trigger_id, source, status, triggered_at, created_at)
		VALUES ('00000000-0000-0000-0000-000000000105', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000011', 'schedule', 'running', now(), now())
	`); !isUniqueViolation(err) {
		t.Fatalf("insert duplicate active after index: got %v, want unique violation", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO autopilot_run (id, autopilot_id, trigger_id, source, status, triggered_at, completed_at, failure_reason, created_at)
		VALUES ('00000000-0000-0000-0000-000000000106', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000011', 'schedule', 'skipped', now(), now(), 'skipped_overlap', now())
	`); err != nil {
		t.Fatalf("insert skipped duplicate after index: %v", err)
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
