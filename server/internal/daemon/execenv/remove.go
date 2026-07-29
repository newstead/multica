package execenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// RemoveAllWritable removes path, retrying after making the tree writable.
// Go's module cache deliberately stores module directories read-only; older
// task envs that carried a private GOMODCACHE can therefore defeat a plain
// os.RemoveAll on Unix, and read-only files can do the same on Windows.
func RemoveAllWritable(path string) error {
	err := os.RemoveAll(path)
	if err == nil || os.IsNotExist(err) {
		return nil
	}

	chmodErr := chmodTreeWritable(path)
	retryErr := os.RemoveAll(path)
	if retryErr == nil || os.IsNotExist(retryErr) {
		return nil
	}
	if chmodErr != nil {
		return fmt.Errorf("%w (chmod retry setup failed: %v)", retryErr, chmodErr)
	}
	return retryErr
}

func chmodTreeWritable(root string) error {
	var firstErr error
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if firstErr == nil {
				firstErr = walkErr
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o600)
		if info.IsDir() {
			mode = 0o700
		}
		if err := os.Chmod(path, mode); err != nil && firstErr == nil {
			firstErr = err
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return firstErr
}
