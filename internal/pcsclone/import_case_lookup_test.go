package pcsclone

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportFutureAliasCaseFollowsFilesystem(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			root := t.TempDir()
			legacyDir, destinationDir := filepath.Join(root, "legacy"), filepath.Join(root, "destination")
			for _, dir := range []string{legacyDir, destinationDir} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			legacy := filepath.Join(legacyDir, defaultConfigName)
			original := []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`)
			if err := os.WriteFile(legacy, original, 0600); err != nil {
				t.Fatal(err)
			}
			actualInfo, err := os.Stat(legacy)
			if err != nil {
				t.Fatal(err)
			}
			alternateInfo, alternateErr := os.Stat(filepath.Join(legacyDir, strings.ToUpper(defaultConfigName)))
			foldsCase := alternateErr == nil && os.SameFile(actualInfo, alternateInfo)
			if alternateErr != nil && !os.IsNotExist(alternateErr) {
				t.Fatal(alternateErr)
			}
			config := filepath.Join(destinationDir, "settings.json")
			if err := os.Symlink(filepath.Join(legacyDir, "RC_REMOTES.JSON"), config); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			args := []string{"config", "import-pcsgo", "--from", legacy}
			if dry {
				args = append(args, "--dry-run")
			}
			code, out, diagnostics := runCLI(t, config, args...)
			if foldsCase {
				if code != 1 || out != "" || !strings.Contains(diagnostics, "separate") {
					t.Errorf("case-insensitive collision accepted: code=%d out=%q stderr=%q", code, out, diagnostics)
				}
			} else if code != 0 || !strings.Contains(out, "Imported 1 account(s)") {
				t.Errorf("distinct case-sensitive target rejected: code=%d out=%q stderr=%q", code, out, diagnostics)
			}
			if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, original) {
				t.Errorf("legacy account changed: data=%q err=%v", data, err)
			}
			if _, err := os.Stat(filepath.Join(legacyDir, "rc_remotes.json")); !os.IsNotExist(err) {
				t.Errorf("legacy alias path was created: %v", err)
			}
		})
	}
}
