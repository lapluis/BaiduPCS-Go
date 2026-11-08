package rc

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func aliasStorageFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previousConfig, previousDir := pcsconfig.Config, pcsconfig.GetConfigDir()
	pcsconfig.Config = pcsconfig.NewConfig(filepath.Join(root, "accounts.json"))
	pcsconfig.SetConfigDir(root)
	t.Cleanup(func() { pcsconfig.Config = previousConfig; pcsconfig.SetConfigDir(previousDir) })
	if err := os.WriteFile(pcsconfig.Config.FilePath(), []byte(`{"fixture":"account retained"}`), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestAliasSavePreservesLinkAndUpdatesTarget(t *testing.T) {
	for _, relative := range []bool{false, true} {
		for _, missing := range []bool{false, true} {
			t.Run(strings.Join([]string{map[bool]string{false: "absolute", true: "relative"}[relative], map[bool]string{false: "existing", true: "missing"}[missing]}, "/"), func(t *testing.T) {
				root := aliasStorageFixture(t)
				target := filepath.Join(root, "actual-remotes.json")
				if !missing {
					if err := os.WriteFile(target, []byte(`{"old":7}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				linkTarget := target
				if relative {
					linkTarget = filepath.Base(target)
				}
				if err := os.Symlink(linkTarget, aliasesPath()); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				r := &Resolver{Aliases: map[string]uint64{"new": 7, "other": 8}}
				if err := r.Save(); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Lstat(aliasesPath()); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("alias symlink was replaced: %v", err)
				}
				data, err := os.ReadFile(target)
				var saved map[string]uint64
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &saved); err != nil || len(saved) != 2 || saved["new"] != 7 || saved["other"] != 8 {
					t.Fatalf("target not updated: %q, %v", data, err)
				}
				reloaded, err := NewResolver(root)
				if err != nil || len(reloaded.Aliases) != 2 || reloaded.Aliases["new"] != 7 {
					t.Fatalf("reload failed: %v", err)
				}
			})
		}
	}
}

func TestAliasSavePreservesExistingPermissions(t *testing.T) {
	aliasStorageFixture(t)
	if err := os.WriteFile(aliasesPath(), []byte(`{"old":7}`), 0640); err != nil {
		t.Fatal(err)
	}
	if err := (&Resolver{Aliases: map[string]uint64{"new": 7}}).Save(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(aliasesPath())
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0640 {
		t.Fatalf("alias permissions changed: %v", info.Mode().Perm())
	}
}

func TestAliasSaveReadOnlyFailurePreservesPreviousFile(t *testing.T) {
	aliasStorageFixture(t)
	original := []byte(`{"old":7}`)
	if err := os.WriteFile(aliasesPath(), original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(aliasesPath(), 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(aliasesPath(), 0600) })
	if file, err := os.OpenFile(aliasesPath(), os.O_RDWR, 0600); err == nil {
		file.Close()
		t.Skip("effective user can write read-only files")
	}
	if err := (&Resolver{Aliases: map[string]uint64{"new": 7}}).Save(); err == nil {
		t.Fatal("read-only alias file was replaced")
	}
	data, err := os.ReadFile(aliasesPath())
	if err != nil || !bytes.Equal(data, original) {
		t.Fatalf("failed save changed previous aliases: %q, %v", data, err)
	}
}

func TestAliasSaveCannotReplaceAccountStorage(t *testing.T) {
	for _, shape := range []string{"same-path", "symlink", "hard-link", "future-symlink"} {
		t.Run(shape, func(t *testing.T) {
			aliasStorageFixture(t)
			account := pcsconfig.Config.FilePath()
			original, err := os.ReadFile(account)
			if err != nil {
				t.Fatal(err)
			}
			if shape == "same-path" {
				pcsconfig.Config = pcsconfig.NewConfig(aliasesPath())
				account = aliasesPath()
				if err := os.WriteFile(account, original, 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if shape == "future-symlink" {
					if err := os.Remove(account); err != nil {
						t.Fatal(err)
					}
				}
				link := os.Symlink
				if shape == "hard-link" {
					link = os.Link
				}
				if err := link(account, aliasesPath()); err != nil {
					t.Skipf("link unavailable: %v", err)
				}
			}
			err = (&Resolver{Aliases: map[string]uint64{"new": 7}}).Save()
			if err == nil || !strings.Contains(err.Error(), "overlap") {
				t.Errorf("account storage accepted as alias target: %v", err)
			}
			got, readErr := os.ReadFile(account)
			if shape == "future-symlink" {
				if !os.IsNotExist(readErr) {
					t.Error("future account storage was created")
				}
			} else if readErr != nil || !bytes.Equal(got, original) {
				t.Fatalf("account storage changed: %q, %v", got, readErr)
			}
		})
	}
}
