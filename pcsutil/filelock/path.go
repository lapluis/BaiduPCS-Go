package filelock

import (
	"errors"
	"os"
	"path/filepath"
)

// CanonicalPath resolves file aliases before deriving an associated lock name.
// A missing final target is allowed when its parent directory already exists.
func CanonicalPath(path string) (string, error) {
	if path == "" {
		return "", errors.New("empty file path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for links := 0; links < 255; links++ {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			info, err := os.Stat(resolved)
			if err != nil {
				return "", err
			}
			if !info.Mode().IsRegular() {
				return "", errors.New("lock target must be a regular file")
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			parent, err := filepath.EvalSymlinks(filepath.Dir(path))
			if err != nil {
				return "", err
			}
			return filepath.Join(parent, filepath.Base(path)), nil
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return "", err
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", errors.New("too many symbolic links in file path")
}
