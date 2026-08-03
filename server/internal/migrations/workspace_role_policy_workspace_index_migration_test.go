package migrations

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestWorkspaceRolePolicyWorkspaceIndexMigrationUpDown verifies migration 267
// — the single-statement CREATE INDEX CONCURRENTLY build for
// workspace_role_policy(workspace_id) that CLAUDE.md requires to live in its
// own migration file — is fully reversible and leaves the index present after
// up. The table itself comes from migration 265 (shared dev database).
func TestWorkspaceRolePolicyWorkspaceIndexMigrationUpDown(t *testing.T) {
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

	// Start clean: ensure the table exists (265) and the index is dropped
	// (267 down is idempotent).
	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.down.sql")
	applyMigrationFile(t, ctx, conn.Conn(), "265_workspace_role_policy.up.sql")
	applyMigrationFile(t, ctx, conn.Conn(), "267_workspace_role_policy_workspace_idx.down.sql")

	var exists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = 'workspace_role_policy_workspace_idx'
		)
	`).Scan(&exists); err != nil {
		t.Fatalf("check index after down: %v", err)
	}
	if exists {
		t.Fatal("workspace_role_policy_workspace_idx still exists after down migration")
	}

	applyMigrationFile(t, ctx, conn.Conn(), "267_workspace_role_policy_workspace_idx.up.sql")

	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = 'workspace_role_policy_workspace_idx'
		)
	`).Scan(&exists); err != nil {
		t.Fatalf("check index after up: %v", err)
	}
	if !exists {
		t.Fatal("workspace_role_policy_workspace_idx missing after up migration")
	}

	// Restore the migrated state (index present) for the rest of the suite.
	applyMigrationFile(t, ctx, conn.Conn(), "267_workspace_role_policy_workspace_idx.down.sql")
	applyMigrationFile(t, ctx, conn.Conn(), "267_workspace_role_policy_workspace_idx.up.sql")
}
