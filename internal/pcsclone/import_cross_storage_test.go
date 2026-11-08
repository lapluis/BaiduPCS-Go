package pcsclone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportRejectsConfigLinkedToLegacyAliases(t *testing.T) {
	for _, exists := range []bool{false, true} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("aliases-exist=%t/dry=%t", exists, dry), func(t *testing.T) {
				root := t.TempDir()
				legacyDir, destinationDir := filepath.Join(root, "legacy"), filepath.Join(root, "destination")
				for _, dir := range []string{legacyDir, destinationDir} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				legacy := filepath.Join(legacyDir, defaultConfigName)
				originalConfig := []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`)
				if err := os.WriteFile(legacy, originalConfig, 0600); err != nil {
					t.Fatal(err)
				}
				aliases := filepath.Join(legacyDir, "rc_remotes.json")
				originalAliases := []byte(`{"old":7}`)
				if exists {
					if err := os.WriteFile(aliases, originalAliases, 0600); err != nil {
						t.Fatal(err)
					}
				}
				config := filepath.Join(destinationDir, "settings.json")
				if err := os.Symlink(aliases, config); err != nil {
					t.Skipf("symbolic links unavailable: %v", err)
				}
				var out, diagnostics bytes.Buffer
				app := &App{CWD: root, Stdout: &out, Stderr: &diagnostics}
				args := []string{"--config", config, "config", "import-pcsgo", "--from", legacy}
				if dry {
					args = append(args, "--dry-run")
				}
				code := app.run(args)
				if code != 1 || app.initialized || out.Len() != 0 || !strings.Contains(diagnostics.String(), "separate") {
					t.Errorf("cross-file storage accepted: code=%d initialized=%t out=%q stderr=%q", code, app.initialized, out.String(), diagnostics.String())
				}
				if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, originalConfig) {
					t.Errorf("legacy account file changed: data=%q err=%v", data, err)
				}
				data, err := os.ReadFile(aliases)
				if exists {
					if err != nil || !bytes.Equal(data, originalAliases) {
						t.Errorf("legacy aliases changed: data=%q err=%v", data, err)
					}
				} else if !os.IsNotExist(err) {
					t.Errorf("legacy aliases unexpectedly created: data=%q err=%v", data, err)
				}
				if _, err := os.Stat(filepath.Join(destinationDir, "rc_remotes.json")); !os.IsNotExist(err) {
					t.Errorf("destination aliases created: %v", err)
				}
			})
		}
	}
}

func TestImportStorageRejectsCrossFileHardLinks(t *testing.T) {
	for _, configToAliases := range []bool{false, true} {
		t.Run(fmt.Sprint(configToAliases), func(t *testing.T) {
			legacyDir, destinationDir := t.TempDir(), t.TempDir()
			legacy, destination := filepath.Join(legacyDir, defaultConfigName), filepath.Join(destinationDir, "settings.json")
			if err := os.WriteFile(legacy, []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
			oldPath, newPath := legacy, filepath.Join(destinationDir, "rc_remotes.json")
			if configToAliases {
				oldPath, newPath = filepath.Join(legacyDir, "rc_remotes.json"), destination
				if err := os.WriteFile(oldPath, []byte(`{"old":7}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Link(oldPath, newPath); err != nil {
				t.Skipf("hard links unavailable: %v", err)
			}
			if err := validateImportStorage(legacy, destination); err == nil || !strings.Contains(err.Error(), "separate") {
				t.Fatalf("cross-file hard link accepted: %v", err)
			}
		})
	}
}

func TestImportAllowsSeparateConfigSymbolicLink(t *testing.T) {
	legacyDir, destinationDir, actualDir := t.TempDir(), t.TempDir(), t.TempDir()
	legacy := filepath.Join(legacyDir, defaultConfigName)
	aliases := filepath.Join(legacyDir, "rc_remotes.json")
	originals := map[string][]byte{
		legacy:  []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`),
		aliases: []byte(`{"old":7}`),
	}
	for path, data := range originals {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	actual, config := filepath.Join(actualDir, "account.json"), filepath.Join(destinationDir, "settings.json")
	if err := os.WriteFile(actual, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, config); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	mustRun(t, config, "config", "import-pcsgo", "--from", legacy)
	for path, want := range originals {
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, want) {
			t.Errorf("independent import changed legacy file: %s data=%q err=%v", path, data, err)
		}
	}
	var imported legacyConfig
	data, err := os.ReadFile(actual)
	if err != nil || json.Unmarshal(data, &imported) != nil || len(imported.BaiduUserList) != 1 || imported.BaiduUserList[0] == nil || imported.BaiduUserList[0].UID != 7 {
		t.Fatalf("independent config link did not receive imported account: err=%v", err)
	}
	if info, err := os.Lstat(config); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("configuration symbolic link changed: info=%v err=%v", info, err)
	}
	var importedAliases map[string]uint64
	data, err = os.ReadFile(filepath.Join(destinationDir, "rc_remotes.json"))
	if err != nil || json.Unmarshal(data, &importedAliases) != nil || importedAliases["old"] != 7 {
		t.Fatalf("independent aliases not imported: aliases=%v err=%v", importedAliases, err)
	}
}

func TestImportRejectsLegacyAliasesLinkedToFutureConfig(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			legacyDir := t.TempDir()
			legacy := filepath.Join(legacyDir, defaultConfigName)
			original := []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`)
			if err := os.WriteFile(legacy, original, 0600); err != nil {
				t.Fatal(err)
			}
			destinationDir := filepath.Join(t.TempDir(), "new", "nested")
			config := filepath.Join(destinationDir, "settings.json")
			aliases := filepath.Join(legacyDir, "rc_remotes.json")
			if err := os.Symlink(config, aliases); err != nil {
				t.Skipf("symbolic links unavailable: %v", err)
			}
			args := []string{"config", "import-pcsgo", "--from", legacy}
			if dry {
				args = append(args, "--dry-run")
			}
			out, diagnostics := requireCode(t, 1, config, args...)
			if out != "" || !strings.Contains(diagnostics, "separate") {
				t.Fatalf("future cross-file storage accepted: out=%q stderr=%q", out, diagnostics)
			}
			if _, err := os.Stat(destinationDir); !os.IsNotExist(err) {
				t.Errorf("future destination was initialized: %v", err)
			}
			if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, original) {
				t.Errorf("legacy account file changed: data=%q err=%v", data, err)
			}
			if target, err := os.Readlink(aliases); err != nil || target != config {
				t.Errorf("legacy alias link changed: target=%q err=%v", target, err)
			}
		})
	}
}
