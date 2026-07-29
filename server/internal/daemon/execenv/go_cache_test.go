package execenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPrepareSharedGoCacheStableAcrossTasks(t *testing.T) {
	t.Parallel()
	workspacesRoot := t.TempDir()

	env1, err := Prepare(PrepareParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    "ws-go-cache",
		TaskID:         "11111111-1111-1111-1111-111111111111",
		AgentName:      "Go Agent",
		Task:           TaskContextForEnv{IssueID: "issue-1"},
	}, testLogger())
	if err != nil {
		t.Fatalf("Prepare env1: %v", err)
	}
	defer env1.Cleanup(true)

	env2, err := Prepare(PrepareParams{
		WorkspacesRoot: workspacesRoot,
		WorkspaceID:    "ws-go-cache",
		TaskID:         "22222222-2222-2222-2222-222222222222",
		AgentName:      "Go Agent",
		Task:           TaskContextForEnv{IssueID: "issue-2"},
	}, testLogger())
	if err != nil {
		t.Fatalf("Prepare env2: %v", err)
	}
	defer env2.Cleanup(true)

	wantMod := filepath.Join(workspacesRoot, ".cache", "go", "mod")
	wantBuild := filepath.Join(workspacesRoot, ".cache", "go", "build")
	if env1.GoModCache != wantMod || env2.GoModCache != wantMod {
		t.Fatalf("GoModCache env1=%q env2=%q, want %q", env1.GoModCache, env2.GoModCache, wantMod)
	}
	if env1.GoBuildCache != wantBuild || env2.GoBuildCache != wantBuild {
		t.Fatalf("GoBuildCache env1=%q env2=%q, want %q", env1.GoBuildCache, env2.GoBuildCache, wantBuild)
	}
	if strings.HasPrefix(env1.GoModCache, env1.RootDir) || strings.HasPrefix(env2.GoModCache, env2.RootDir) {
		t.Fatalf("shared Go module cache must not live under a task root: env1=%q env2=%q", env1.GoModCache, env2.GoModCache)
	}
	for _, dir := range []string{wantMod, wantBuild} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("shared Go cache dir %s not created: info=%v err=%v", dir, info, err)
		}
	}
	goEnv := GoCacheEnv(env1.GoModCache, env1.GoBuildCache)
	if goEnv["GOMODCACHE"] != wantMod || goEnv["GOCACHE"] != wantBuild {
		t.Fatalf("GoCacheEnv = %#v, want GOMODCACHE=%q GOCACHE=%q", goEnv, wantMod, wantBuild)
	}

	if err := env1.Cleanup(true); err != nil {
		t.Fatalf("cleanup env1: %v", err)
	}
	if _, err := os.Stat(wantMod); err != nil {
		t.Fatalf("shared Go module cache should survive task cleanup: %v", err)
	}
}

func TestPrepareCodexIncludesSharedGoCachesInWritableRoots(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Codex workspace-write writable_roots are Linux-specific")
	}
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("CODEX_HOME", filepath.Join(fakeHome, ".codex"))

	env, err := Prepare(PrepareParams{
		WorkspacesRoot: t.TempDir(),
		WorkspaceID:    "ws-codex-go-cache",
		TaskID:         "33333333-3333-3333-3333-333333333333",
		AgentName:      "Codex Agent",
		Provider:       "codex",
		CodexVersion:   "0.121.0",
		Task:           TaskContextForEnv{IssueID: "issue-3"},
	}, testLogger())
	if err != nil {
		t.Fatalf("Prepare codex env: %v", err)
	}
	defer env.Cleanup(true)

	data, err := os.ReadFile(filepath.Join(env.CodexHome, "config.toml"))
	if err != nil {
		t.Fatalf("read codex config: %v", err)
	}
	config := string(data)
	for _, want := range []string{env.TaskHome, env.GoModCache, env.GoBuildCache} {
		if want == "" || !strings.Contains(config, want) {
			t.Fatalf("codex config missing writable root %q:\n%s", want, config)
		}
	}
}

func TestRemoveAllWritableRemovesReadOnlyGoModuleTree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	modDir := filepath.Join(root, "pkg", "mod", "example.com", "mod@v1.0.0")
	if err := os.MkdirAll(filepath.Join(modDir, "subpkg"), 0o755); err != nil {
		t.Fatalf("mkdir module tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "subpkg", "go.mod"), []byte("module example.com/mod\n"), 0o444); err != nil {
		t.Fatalf("write module file: %v", err)
	}
	for _, dir := range []string{filepath.Join(modDir, "subpkg"), modDir} {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("chmod %s: %v", dir, err)
		}
	}
	t.Cleanup(func() { _ = chmodTreeWritable(root) })

	if err := RemoveAllWritable(root); err != nil {
		t.Fatalf("RemoveAllWritable: %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("root should be removed, stat err=%v", err)
	}
}
