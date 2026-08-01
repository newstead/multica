package execenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// GoCachePaths are daemon-owned cache directories shared by task environments.
// They intentionally live outside each task env root so Go module downloads and
// build artifacts survive task GC and can be reused by later tasks.
type GoCachePaths struct {
	ModCache   string
	BuildCache string
}

func prepareSharedGoCache(workspacesRoot string) (GoCachePaths, error) {
	if workspacesRoot == "" {
		return GoCachePaths{}, fmt.Errorf("execenv: workspaces root is required")
	}
	paths := GoCachePaths{
		ModCache:   filepath.Join(workspacesRoot, ".cache", "go", "mod"),
		BuildCache: filepath.Join(workspacesRoot, ".cache", "go", "build"),
	}
	for _, dir := range []string{paths.ModCache, paths.BuildCache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return GoCachePaths{}, fmt.Errorf("execenv: create shared go cache %s: %w", dir, err)
		}
	}
	return paths, nil
}

// GoCacheEnv returns environment overrides that keep Go's module and build
// caches daemon-shared while leaving GOPATH itself task/user scoped.
func GoCacheEnv(modCache, buildCache string) map[string]string {
	env := map[string]string{}
	if modCache != "" {
		env["GOMODCACHE"] = modCache
	}
	if buildCache != "" {
		env["GOCACHE"] = buildCache
	}
	return env
}

func goCacheWritableRoots(paths GoCachePaths) []string {
	roots := make([]string, 0, 2)
	if paths.ModCache != "" {
		roots = append(roots, paths.ModCache)
	}
	if paths.BuildCache != "" {
		roots = append(roots, paths.BuildCache)
	}
	return roots
}
