package pcsclone

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func sameStoragePath(a, b string) (bool, error) {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(strings.TrimRight(a, " ."), strings.TrimRight(b, " .")), nil
	}
	if a == b {
		return true, nil
	}
	if !strings.EqualFold(filepath.Base(a), filepath.Base(b)) {
		return false, nil
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	for _, err := range []error{aErr, bErr} {
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	if aErr == nil || bErr == nil {
		return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo), nil
	}
	aDir, bDir := filepath.Dir(a), filepath.Dir(b)
	aParent, aErr := os.Stat(aDir)
	bParent, bErr := os.Stat(bDir)
	for _, err := range []error{aErr, bErr} {
		if err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	if aErr == nil && bErr == nil {
		if !os.SameFile(aParent, bParent) {
			return false, nil
		}
		return storageDirectoryFoldsCase(aDir)
	}
	if aErr == nil || bErr == nil {
		return false, nil // The filesystem distinguishes the existing parents.
	}
	if strings.EqualFold(a, b) {
		return false, ambiguousStorageCase(aDir)
	}
	return false, nil
}

// Use existing entries as read-only evidence. In particular, a legacy account
// directory must not be modified just to probe its filename lookup rules.
func storageDirectoryFoldsCase(dir string) (bool, error) {
	return inspectStorageDirectoryCase(dir, os.Lstat)
}

func inspectStorageDirectoryCase(dir string, lstat func(string) (os.FileInfo, error)) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		names[entry.Name()] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		alternate := []byte(name)
		for i, c := range alternate {
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
				alternate[i] = c ^ 32
				break
			}
		}
		if string(alternate) == name {
			continue
		}
		if names[string(alternate)] {
			return false, nil // Distinct entries, even if they are hard links.
		}
		original, err := lstat(filepath.Join(dir, name))
		if err != nil {
			return false, err
		}
		// Capture lazy file IDs before another process can replace the path.
		if !os.SameFile(original, original) {
			return false, fmt.Errorf("cannot capture configuration case reference identity")
		}
		probe, probeErr := lstat(filepath.Join(dir, string(alternate)))
		current, err := lstat(filepath.Join(dir, name))
		if err != nil {
			return false, fmt.Errorf("configuration case reference changed: %w", err)
		}
		if !os.SameFile(original, current) {
			return false, fmt.Errorf("configuration case reference changed during inspection")
		}
		if os.IsNotExist(probeErr) {
			return false, nil
		}
		if probeErr != nil {
			return false, probeErr
		}
		if !os.SameFile(original, probe) {
			return false, fmt.Errorf("configuration case lookup changed during inspection")
		}
		return true, nil
	}
	return false, ambiguousStorageCase(dir)
}

func ambiguousStorageCase(dir string) error {
	return fmt.Errorf("cannot verify filesystem case behavior for configuration storage %q; choose names that differ beyond letter case", dir)
}
