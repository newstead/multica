package execenv

import (
	"os"
	"path/filepath"
	"testing"
)

// Codex tasks on Linux run inside the workspace-write sandbox, so the daemon
// redirects HOME/XDG into a seeded per-task `home/` directory under the env
// root (MUL-4856) and grants it write access via the sandbox writable_roots.
//
// These tests pin the two halves that are easy to regress: the per-task home
// is created under the env root on Linux, and the task-scoped CODEX_HOME — a
// separate mechanism — must not be confused with it.

// legacyTaskHomeDirName is the directory the daemon creates under the env root
// to hold the redirected HOME for Linux Codex tasks.
const legacyTaskHomeDirName = "home"

func TestPrepareCodexCreatesTaskHomeAndScopesCodexHome(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	sharedHome := t.TempDir()
	t.Setenv("CODEX_HOME", sharedHome)

	env, err := Prepare(PrepareParams{
		WorkspacesRoot: t.TempDir(),
		WorkspaceID:    "ws-codex-real-home",
		TaskID:         "b1c2d3e4-f5a6-7890-bcde-f01234567890",
		AgentName:      "Codex Agent",
		Provider:       "codex",
		Task:           TaskContextForEnv{IssueID: "real-home-test"},
	}, testLogger())
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	defer env.Cleanup(true)

	if _, err := os.Lstat(filepath.Join(env.RootDir, legacyTaskHomeDirName)); err != nil {
		t.Errorf("per-task HOME directory must be created under the env root (err=%v)", err)
	}
	if env.TaskHome == "" {
		t.Error("expected TaskHome to be set for a Linux Codex task")
	}

	// The task-scoped CODEX_HOME is a different mechanism and stays: it is
	// managed Codex state (config, sessions, skills, MCP), not the Unix HOME.
	if want := filepath.Join(env.RootDir, codexHomeDirName); env.CodexHome != want {
		t.Errorf("CodexHome = %q, want %q", env.CodexHome, want)
	}
	if _, err := os.Stat(env.CodexHome); err != nil {
		t.Errorf("task-scoped CODEX_HOME should exist: %v", err)
	}
}

// TestReuseCodexToleratesLegacyTaskHome covers an env root prepared by an older
// daemon: its leftover `home/` directory (including the credential symlinks it
// seeded) must be reused in place rather than break the reuse path.
func TestReuseCodexToleratesLegacyTaskHome(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv.

	sharedHome := t.TempDir()
	t.Setenv("CODEX_HOME", sharedHome)

	env, err := Prepare(PrepareParams{
		WorkspacesRoot: t.TempDir(),
		WorkspaceID:    "ws-codex-legacy-home",
		TaskID:         "c1d2e3f4-a5b6-7890-cdef-012345678901",
		AgentName:      "Codex Agent",
		Provider:       "codex",
		Task:           TaskContextForEnv{IssueID: "legacy-home-test"},
	}, testLogger())
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	defer env.Cleanup(true)

	// Simulate what a previous daemon left behind.
	legacyHome := filepath.Join(env.RootDir, legacyTaskHomeDirName)
	if err := os.MkdirAll(filepath.Join(legacyHome, ".config"), 0o700); err != nil {
		t.Fatalf("seed legacy task home: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyHome, ".npmrc"), []byte("//registry:_authToken=x\n"), 0o600); err != nil {
		t.Fatalf("seed legacy .npmrc: %v", err)
	}

	reused := Reuse(ReuseParams{
		WorkDir:  env.WorkDir,
		Provider: "codex",
		Task:     TaskContextForEnv{IssueID: "legacy-home-test"},
	}, testLogger())
	if reused == nil {
		t.Fatal("Reuse returned nil on an env root carrying a legacy task home")
	}
	if reused.CodexHome == "" {
		t.Error("expected CodexHome to be restored on reuse")
	}
}
