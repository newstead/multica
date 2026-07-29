package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestRunMigrationsAppliesUpstreamDuplicatePrefixesAfterForkVersions(t *testing.T) {
	pool := openDisposableMigrationDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if _, err := pool.Exec(ctx, `
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			agent_id UUID NOT NULL,
			completed_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			status TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create agent_task_queue fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		t.Fatalf("create schema_migrations fixture: %v", err)
	}
	for _, version := range []string{
		"233_task_usage_reasoning_tokens",
		"234_llm_usage_cost_backfill",
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			t.Fatalf("seed fork migration %s: %v", version, err)
		}
	}

	wantVersions := []string{
		"233_agent_task_queue_agent_terminal_latest_index",
		"233_task_usage_reasoning_tokens",
		"234_agent_task_queue_retired_session_id",
		"234_llm_usage_cost_backfill",
	}
	files := realMigrationFilesForVersions(t, wantVersions)
	if got := migrationVersions(files); !equalStrings(got, wantVersions) {
		t.Fatalf("selected migration files = %v, want %v", got, wantVersions)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           files,
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}); err != nil {
		t.Fatalf("run duplicate-prefix migrations after fork versions: %v", err)
	}

	if got := schemaMigrationVersions(t, ctx, pool); !equalStrings(got, wantVersions) {
		t.Fatalf("schema_migrations after duplicate-prefix upgrade = %v, want %v", got, wantVersions)
	}
	if !publicIndexExists(t, ctx, pool, "idx_agent_task_queue_agent_terminal_latest") {
		t.Fatal("upstream 233 terminal-task index was not created")
	}
	if !publicColumnExists(t, ctx, pool, "agent_task_queue", "retired_session_id") {
		t.Fatal("upstream 234 retired_session_id column was not created")
	}
}

func openDisposableMigrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	admin := openTestPool(t)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}

	name := fmt.Sprintf("migrate_dup_prefix_%d_%d", time.Now().UnixNano(), rand.Uint32())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`CREATE DATABASE %s`, pgx.Identifier{name}.Sanitize())); err != nil {
		t.Skipf("create disposable migration database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name); err != nil {
			t.Logf("terminate connections to %s: %v", name, err)
		}
		if _, err := admin.Exec(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS %s`, pgx.Identifier{name}.Sanitize())); err != nil {
			t.Logf("drop disposable migration database %s: %v", name, err)
		}
	})

	pool, err := pgxpool.New(ctx, replaceMigrationDatabase(dbURL, name))
	if err != nil {
		t.Fatalf("connect disposable migration database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping disposable migration database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func replaceMigrationDatabase(url, name string) string {
	idx := strings.LastIndex(url, "/")
	if idx < 0 {
		return url
	}
	rest := url[idx+1:]
	q := strings.Index(rest, "?")
	if q < 0 {
		return url[:idx+1] + name
	}
	return url[:idx+1] + name + rest[q:]
}

func realMigrationFilesForVersions(t *testing.T, wantVersions []string) []string {
	t.Helper()
	want := make(map[string]bool, len(wantVersions))
	for _, version := range wantVersions {
		want[version] = true
	}
	all, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("discover real migrations: %v", err)
	}
	var files []string
	for _, file := range all {
		if want[migrations.ExtractVersion(file)] {
			files = append(files, file)
		}
	}
	if len(files) != len(wantVersions) {
		t.Fatalf("discovered %d duplicate-prefix migration files, want %d: %v", len(files), len(wantVersions), migrationVersions(files))
	}
	return files
}

func migrationVersions(files []string) []string {
	versions := make([]string, len(files))
	for i, file := range files {
		versions[i] = strings.TrimSuffix(filepath.Base(file), ".up.sql")
	}
	return versions
}

func schemaMigrationVersions(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	defer rows.Close()
	var versions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("schema_migrations rows: %v", err)
	}
	sort.Strings(versions)
	return versions
}

func publicIndexExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND indexname = $1
		)
	`, name).Scan(&exists); err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	return exists
}

func publicColumnExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table, column string) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)
	`, table, column).Scan(&exists); err != nil {
		t.Fatalf("check column %s.%s: %v", table, column, err)
	}
	return exists
}
