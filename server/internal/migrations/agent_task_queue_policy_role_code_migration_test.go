package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestAgentTaskQueuePolicyRoleCodeMigrationUpDown verifies migration 266 is
// additive and fully reversible: up adds the audit column policy_role_code,
// down removes it. The column is the ROLEPOL-3 audit/evidence field recording
// WHICH role rule routed a task at enqueue time (NULL = agent_default).
func TestAgentTaskQueuePolicyRoleCodeMigrationUpDown(t *testing.T) {
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

	// Start clean: the down migration is idempotent.
	applyMigrationFile(t, ctx, conn.Conn(), "266_agent_task_queue_policy_role_code.down.sql")

	applyMigrationFile(t, ctx, conn.Conn(), "266_agent_task_queue_policy_role_code.up.sql")

	var exists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'agent_task_queue' AND column_name = 'policy_role_code'
		)
	`).Scan(&exists); err != nil {
		t.Fatalf("check policy_role_code column: %v", err)
	}
	if !exists {
		t.Fatal("agent_task_queue.policy_role_code missing after up migration")
	}

	// The column accepts the canonical role codes and NULL.
	if _, err := conn.Exec(ctx, `
		UPDATE agent_task_queue SET policy_role_code = 'QA' WHERE id = (SELECT id FROM agent_task_queue LIMIT 1)
	`); err != nil {
		t.Fatalf("set policy_role_code: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "266_agent_task_queue_policy_role_code.down.sql")

	var existsAfter bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'agent_task_queue' AND column_name = 'policy_role_code'
		)
	`).Scan(&existsAfter); err != nil {
		t.Fatalf("check policy_role_code column after down: %v", err)
	}
	if existsAfter {
		t.Fatal("agent_task_queue.policy_role_code still exists after down migration")
	}

	// Restore the migrated state: the migration tests share the dev database
	// with the rest of the suite, which expects the latest schema applied.
	applyMigrationFile(t, ctx, conn.Conn(), "266_agent_task_queue_policy_role_code.up.sql")
}
