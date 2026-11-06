package rc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ValidateLogPath rejects logs that could change an input, output or state file.
// Call it before creating the log or starting an operation. Paths are endpoints;
// remote endpoints do not overlap a local log.
func ValidateLogPath(ctx context.Context, cwd, logPath string, paths ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	localPath := func(p string) (string, error) {
		if filepath.VolumeName(p) != "" && !filepath.IsAbs(p) {
			return "", fmt.Errorf("drive-relative path is not supported: %s", p)
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(cwd, p)
		}
		return filepath.Abs(p)
	}
	logPath, err := localPath(logPath)
	if err != nil {
		return err
	}
	// Local operations already reject symbolic links. Apply the same rule to
	// logs, including their parents, before following them with OpenFile.
	if err := safeParents(logPath); err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	logInfo, err := os.Stat(logPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	checkLinks := false
	if logInfo != nil && logInfo.Mode().IsRegular() {
		checkLinks, err = logHasMultipleLinks(logPath, logInfo)
		if err != nil {
			return fmt.Errorf("log file links: %w", err)
		}
	}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == "" || IsRemote(p) {
			continue
		}
		p, err = localPath(p)
		if err != nil {
			return err
		}
		if err := validatePair(Endpoint{FS: &Local{}, Root: logPath}, Endpoint{FS: &Local{}, Root: p}); err != nil {
			return fmt.Errorf("log file overlaps or cannot be checked against %q: %w", p, err)
		}
		if !checkLinks {
			continue
		}
		info, err := os.Stat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			continue
		}
		// An external hard link to a descendant has no common path prefix.
		// Scan only for logs with multiple links, keeping normal startup cheap.
		if err := filepath.WalkDir(p, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if os.SameFile(logInfo, info) {
				return fmt.Errorf("log file overlaps %q (hard link)", path)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return ctx.Err()
}
