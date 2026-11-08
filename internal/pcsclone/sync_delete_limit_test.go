package pcsclone

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncDeletionLimitIncludesExplicitlyExcludedFiles(t *testing.T) {
	for _, tc := range []struct {
		deleteExcluded bool
		limit          int
		blocked        bool
	}{
		{false, 0, true},
		{false, 1, false},
		{true, 1, true},
		{true, 2, false},
	} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("delete-excluded=%t/limit=%d/dry=%t", tc.deleteExcluded, tc.limit, dry), func(t *testing.T) {
				root := t.TempDir()
				source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
				for _, dir := range []string{source, target} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				originals := map[string]string{
					filepath.Join(source, "new.txt"):   "new source content",
					filepath.Join(source, "keep.tmp"):  "excluded source content",
					filepath.Join(target, "keep.tmp"):  "unique excluded target content",
					filepath.Join(target, "extra.txt"): "extra target content",
				}
				for path, data := range originals {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{"sync", source, target, "--exclude", "*.tmp", "--max-delete", fmt.Sprint(tc.limit)}
				if tc.deleteExcluded {
					args = append(args, "--delete-excluded")
				}
				if dry {
					args = append(args, "--dry-run")
				}
				code, _, diagnostics := runCLI(t, filepath.Join(root, "config", defaultConfigName), args...)
				if tc.blocked {
					if code != 1 || !strings.Contains(diagnostics, "exceed --max-delete") || strings.Contains(diagnostics, "copy:") || strings.Contains(diagnostics, "delete:") {
						t.Errorf("limit did not block before operations: code=%d stderr=%q", code, diagnostics)
					}
				} else if code != 0 {
					t.Errorf("allowed sync failed: code=%d stderr=%q", code, diagnostics)
				}
				for path, want := range originals {
					deleted := !tc.blocked && !dry && (path == filepath.Join(target, "extra.txt") || tc.deleteExcluded && path == filepath.Join(target, "keep.tmp"))
					data, err := os.ReadFile(path)
					if deleted {
						if !os.IsNotExist(err) {
							t.Errorf("selected target retained: %s err=%v", path, err)
						}
					} else if err != nil || string(data) != want {
						t.Errorf("protected file changed: %s data=%q err=%v", path, data, err)
					}
				}
				data, err := os.ReadFile(filepath.Join(target, "new.txt"))
				if tc.blocked || dry {
					if !os.IsNotExist(err) {
						t.Errorf("blocked/preview sync copied a file: data=%q err=%v", data, err)
					}
				} else if err != nil || string(data) != "new source content" {
					t.Errorf("allowed sync lost copy: data=%q err=%v", data, err)
				}
			})
		}
	}
}
