// Package filelock provides persistent, process-scoped lock files. Keep the
// pathname after Close so contenders cannot accidentally lock different inodes.
package filelock

import (
	"os"
	"path/filepath"
)

func TryLock(path string) (*os.File, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return tryLock(path)
}
