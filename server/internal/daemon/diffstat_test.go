package daemon

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCollectTaskDiffStatsAggregatesTrackedAndUntrackedChanges(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	repo := filepath.Join(workDir, "repo")
	runDiffStatTestGit(t, workDir, "init", "-b", "main", repo)
	writeDiffStatTestFile(t, filepath.Join(repo, "tracked.txt"), "one\ntwo\n")
	runDiffStatTestGit(t, repo, "add", "tracked.txt")
	runDiffStatTestGit(t, repo, "commit", "-m", "initial")
	runDiffStatTestGit(t, repo, "checkout", "-b", "agent/test", "main")

	writeDiffStatTestFile(t, filepath.Join(repo, "tracked.txt"), "one\nchanged\nthree\n")
	writeDiffStatTestFile(t, filepath.Join(repo, "added.txt"), "alpha\nbeta\n")
	runDiffStatTestGit(t, repo, "add", "added.txt")
	writeDiffStatTestFile(t, filepath.Join(repo, "untracked.txt"), "loose\n")

	got := collectTaskDiffStats(workDir, slog.Default())
	want := DiffStats{Additions: 5, Deletions: 1, FilesChanged: 3}
	if got != want {
		t.Fatalf("diff stats = %+v, want %+v", got, want)
	}
}

func TestCollectTaskDiffStatsDiscoversMultipleWorktrees(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	for _, name := range []string{"repo-a", "nested/repo-b"} {
		repo := filepath.Join(workDir, name)
		runDiffStatTestGit(t, workDir, "init", "-b", "main", repo)
		writeDiffStatTestFile(t, filepath.Join(repo, "file.txt"), "base\n")
		runDiffStatTestGit(t, repo, "add", "file.txt")
		runDiffStatTestGit(t, repo, "commit", "-m", "initial")
		runDiffStatTestGit(t, repo, "checkout", "-b", "agent/test", "main")
		writeDiffStatTestFile(t, filepath.Join(repo, "untracked.txt"), "one\n")
	}

	got := collectTaskDiffStats(workDir, slog.Default())
	want := DiffStats{Additions: 2, FilesChanged: 2}
	if got != want {
		t.Fatalf("diff stats = %+v, want %+v", got, want)
	}
}

func runDiffStatTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %s: %v", args, out, err)
	}
}

func writeDiffStatTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
