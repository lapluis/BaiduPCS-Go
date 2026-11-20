package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsupload"
)

func localLogPath(cwd, path string) (string, error) {
	path, err := ResolveLocalPath(cwd, path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// ValidateCommandLogPath protects both command data and its runtime storage.
// A file endpoint's download state lives beside the file, outside its own path.
func ValidateCommandLogPath(ctx context.Context, cwd, logPath, configFile, filesFrom string, endpoints ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	configDir := pcsconfig.GetConfigDir()
	if configFile != "" {
		configDir = filepath.Dir(configFile)
	}
	var protected []string
	for _, file := range []string{configFile, filesFrom,
		filepath.Join(configDir, "rc_remotes.json"),
		filepath.Join(configDir, "rc-state"),
		filepath.Join(configDir, pcsupload.UploadingFileName),
		filepath.Join(configDir, pcsupload.UploadingFileName+".lock")} {
		if file == "" {
			continue
		}
		path, err := localLogPath(cwd, file)
		if err != nil {
			return err
		}
		protected = append(protected, path)
	}
	for _, endpoint := range endpoints {
		if endpoint == "" || IsRemote(endpoint) {
			continue
		}
		path, err := localLogPath(cwd, endpoint)
		if err != nil {
			return err
		}
		protected = append(protected, path, filepath.Join(filepath.Dir(path), ".pcsclone-partials"))
	}
	return ValidateLogPath(ctx, cwd, logPath, protected...)
}

// ValidateLogPath rejects logs that could change an input, output or state file.
// Call it before creating the log or starting an operation. Paths are endpoints;
// remote endpoints do not overlap a local log.
func ValidateLogPath(ctx context.Context, cwd, logPath string, paths ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	logPath, err := localLogPath(cwd, logPath)
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
		checkLinks, err = fileHasMultipleLinks(logPath, logInfo)
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
		p, err = localLogPath(cwd, p)
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
		// Read the root already confirmed by Stat as a directory: WalkDir would
		// inspect it with Lstat and skip a Windows junction. Descendant links
		// remain untraversed, avoiding cycles through linked directories.
		var walk func(string) error
		walk = func(dir string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				path := filepath.Join(dir, entry.Name())
				if entry.IsDir() {
					if err := walk(path); err != nil {
						return err
					}
					continue
				}
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				if os.SameFile(logInfo, info) {
					return fmt.Errorf("log file overlaps %q (hard link)", path)
				}
			}
			return nil
		}
		if err := walk(p); err != nil {
			return err
		}
	}
	return ctx.Err()
}
