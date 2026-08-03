package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestWorkspaceRolePolicyMigrationUpDown verifies migration 265 is additive
// (new table + columns) and fully reversible: up creates the matrix and the
// task policy columns with their CHECK constraints, down removes them.
func TestWorkspaceRolePolicyMigrationUpDown(t *testing.T) {
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
	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.down.sql")

	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.up.sql")

	for _, col := range []string{"policy_model", "policy_thinking_level", "policy_service_tier"} {
		var exists bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'agent_task_queue' AND column_name = $1
			)
		`, col).Scan(&exists); err != nil {
			t.Fatalf("check column %s: %v", col, err)
		}
		if !exists {
			t.Fatalf("agent_task_queue.%s missing after up migration", col)
		}
	}

	var enabledExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'workspace' AND column_name = 'role_policy_enabled'
		)
	`).Scan(&enabledExists); err != nil {
		t.Fatalf("check workspace.role_policy_enabled: %v", err)
	}
	if !enabledExists {
		t.Fatal("workspace.role_policy_enabled missing after up migration")
	}

	var tableExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'workspace_role_policy'
		)
	`).Scan(&tableExists); err != nil {
		t.Fatalf("check workspace_role_policy table: %v", err)
	}
	if !tableExists {
		t.Fatal("workspace_role_policy table missing after up migration")
	}

	// Use a real workspace row so the FK on workspace_id does not mask the
	// CHECK violations we are asserting (Postgres may report either code).
	var workspaceID string
	if err := conn.QueryRow(ctx, `SELECT id::text FROM workspace LIMIT 1`).Scan(&workspaceID); err != nil {
		t.Fatalf("load fixture workspace: %v", err)
	}

	// The role_code CHECK rejects unknown roles.
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace_role_policy (workspace_id, role_code, fallback)
		VALUES ($1, 'ZZ', 'agent_default')
	`, workspaceID); !isCheckViolation(err) {
		t.Fatalf("unknown role_code insert: got %v, want check violation", err)
	}

	// The fallback CHECK rejects unknown fallbacks.
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace_role_policy (workspace_id, role_code, fallback)
		VALUES ($1, 'BE', 'bogus')
	`, workspaceID); !isCheckViolation(err) {
		t.Fatalf("unknown fallback insert: got %v, want check violation", err)
	}

	// The XOR CHECK rejects agent_id combined with exec fields.
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace_role_policy (workspace_id, role_code, agent_id, model, fallback)
		VALUES ($1, 'BE', $2, 'gpt-5', 'agent_default')
	`, workspaceID, "00000000-0000-0000-0000-000000000002"); !isCheckViolation(err) {
		t.Fatalf("agent+exec insert: got %v, want check violation", err)
	}

	// A valid rule inserts fine.
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace_role_policy (workspace_id, role_code, fallback)
		VALUES ($1, 'BE', 'agent_default')
	`, workspaceID); err != nil {
		t.Fatalf("valid rule insert: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		DELETE FROM workspace_role_policy WHERE workspace_id = $1
	`, workspaceID); err != nil {
		t.Fatalf("cleanup rule: %v", err)
	}

	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.down.sql")

	for _, col := range []string{"policy_model", "policy_thinking_level", "policy_service_tier"} {
		var exists bool
		if err := conn.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'agent_task_queue' AND column_name = $1
			)
		`, col).Scan(&exists); err != nil {
			t.Fatalf("check column %s after down: %v", col, err)
		}
		if exists {
			t.Fatalf("agent_task_queue.%s still exists after down migration", col)
		}
	}

	var tableExistsAfter bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_name = 'workspace_role_policy'
		)
	`).Scan(&tableExistsAfter); err != nil {
		t.Fatalf("check table after down: %v", err)
	}
	if tableExistsAfter {
		t.Fatal("workspace_role_policy table still exists after down migration")
	}

	// Restore the migrated state: the migration tests share the dev database
	// with the rest of the suite, which expects the latest schema applied
	// (other tests insert agent_task_queue rows with the policy columns).
	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.up.sql")
}
