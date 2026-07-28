package daemon

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const diffStatTimeout = 15 * time.Second

var shortStatPattern = regexp.MustCompile(`(\d+) files? changed|(\d+) insertions?\(\+\)|(\d+) deletions?\(-\)`)

// DiffStats is the per-task aggregate of code changes across checked-out repos.
type DiffStats struct {
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	FilesChanged int `json:"files_changed"`
}

func collectTaskDiffStats(workDir string, logger *slog.Logger) DiffStats {
	var total DiffStats
	if workDir == "" {
		return total
	}

	ctx, cancel := context.WithTimeout(context.Background(), diffStatTimeout)
	defer cancel()

	worktrees, err := discoverGitWorktrees(workDir)
	if err != nil {
		if logger != nil {
			logger.Warn("diffstat: discover worktrees failed", "work_dir", workDir, "error", err)
		}
		return total
	}

	for _, worktree := range worktrees {
		if ctx.Err() != nil {
			if logger != nil {
				logger.Warn("diffstat: collection timed out", "work_dir", workDir, "error", ctx.Err())
			}
			return total
		}
		stats, err := collectWorktreeDiffStats(ctx, worktree)
		if err != nil {
			if logger != nil {
				logger.Warn("diffstat: collect worktree failed", "worktree", worktree, "error", err)
			}
			continue
		}
		total.Additions += stats.Additions
		total.Deletions += stats.Deletions
		total.FilesChanged += stats.FilesChanged
	}
	return total
}

func discoverGitWorktrees(workDir string) ([]string, error) {
	var worktrees []string
	err := filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
			worktrees = append(worktrees, path)
			return filepath.SkipDir
		} else if !os.IsNotExist(err) {
			return err
		}
		return nil
	})
	return worktrees, err
}

func collectWorktreeDiffStats(ctx context.Context, worktree string) (DiffStats, error) {
	baseRef, err := resolveDiffBaseRef(ctx, worktree)
	if err != nil {
		return DiffStats{}, err
	}

	var total DiffStats
	out, err := runDiffGit(ctx, worktree, "diff", "--shortstat", baseRef+"...HEAD")
	if err != nil {
		return DiffStats{}, fmt.Errorf("git diff base: %s: %w", strings.TrimSpace(string(out)), err)
	}
	total.add(parseShortStat(out))

	out, err = runDiffGit(ctx, worktree, "diff", "--shortstat", "HEAD")
	if err != nil {
		return DiffStats{}, fmt.Errorf("git diff worktree: %s: %w", strings.TrimSpace(string(out)), err)
	}
	total.add(parseShortStat(out))

	untracked, err := untrackedFileStats(ctx, worktree)
	if err != nil {
		return DiffStats{}, err
	}
	total.add(untracked)
	return total, nil
}

func resolveDiffBaseRef(ctx context.Context, worktree string) (string, error) {
	out, err := runDiffGit(ctx, worktree, "reflog", "show", "--format=%gs", "-n", "20", "HEAD")
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			ref, ok := strings.CutPrefix(strings.TrimSpace(line), "branch: Created from ")
			if !ok || strings.TrimSpace(ref) == "" {
				continue
			}
			ref = strings.TrimSpace(ref)
			if _, err := runDiffGit(ctx, worktree, "rev-parse", "--verify", ref+"^{commit}"); err == nil {
				return ref, nil
			}
		}
	}

	for _, ref := range []string{"refs/remotes/origin/HEAD", "refs/remotes/origin/main", "refs/remotes/origin/master", "main", "master"} {
		if _, err := runDiffGit(ctx, worktree, "rev-parse", "--verify", ref+"^{commit}"); err == nil {
			return ref, nil
		}
	}
	return "", fmt.Errorf("resolve diff base ref: no usable base ref")
}

func untrackedFileStats(ctx context.Context, worktree string) (DiffStats, error) {
	out, err := runDiffGit(ctx, worktree, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return DiffStats{}, fmt.Errorf("git status porcelain: %s: %w", strings.TrimSpace(string(out)), err)
	}

	var stats DiffStats
	for _, entry := range bytes.Split(out, []byte{0}) {
		if !bytes.HasPrefix(entry, []byte("?? ")) {
			continue
		}
		rel := string(entry[3:])
		if rel == "" {
			continue
		}
		additions, err := countFileLines(filepath.Join(worktree, filepath.FromSlash(rel)))
		if err != nil {
			return DiffStats{}, err
		}
		stats.FilesChanged++
		stats.Additions += additions
	}
	return stats, nil
}

func countFileLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read untracked file %s: %w", path, err)
	}
	if bytes.Contains(data, []byte{0}) {
		return 0, nil
	}
	lines := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	return lines, nil
}

func parseShortStat(out []byte) DiffStats {
	var stats DiffStats
	for _, match := range shortStatPattern.FindAllStringSubmatch(string(out), -1) {
		switch {
		case match[1] != "":
			stats.FilesChanged = atoiDefaultZero(match[1])
		case match[2] != "":
			stats.Additions = atoiDefaultZero(match[2])
		case match[3] != "":
			stats.Deletions = atoiDefaultZero(match[3])
		}
	}
	return stats
}

func (s *DiffStats) add(other DiffStats) {
	s.Additions += other.Additions
	s.Deletions += other.Deletions
	s.FilesChanged += other.FilesChanged
}

func atoiDefaultZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func diffGitEnv() []string {
	base := os.Environ()
	existing := 0
	for _, e := range base {
		if strings.HasPrefix(e, "GIT_CONFIG_COUNT=") {
			if n, err := strconv.Atoi(strings.TrimPrefix(e, "GIT_CONFIG_COUNT=")); err == nil {
				existing = n
			}
		}
	}

	idx := strconv.Itoa(existing)
	return append(base,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT="+strconv.Itoa(existing+1),
		"GIT_CONFIG_KEY_"+idx+"=safe.directory",
		"GIT_CONFIG_VALUE_"+idx+"=*",
	)
}

func runDiffGit(ctx context.Context, worktree string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", worktree}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = diffGitEnv()
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("git command timed out after %s: %w", diffStatTimeout, ctx.Err())
	}
	return out, err
}
