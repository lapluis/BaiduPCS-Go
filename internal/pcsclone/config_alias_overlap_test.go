package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigCannotOverwriteAliasStorage(t *testing.T) {
	for _, mode := range []string{"same-path", "hard-link", "symbolic-link", "dangling-symbolic-link"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			aliases := filepath.Join(root, "rc_remotes.json")
			original := []byte(`{"aa":7,"bb":8}`)
			if mode != "dangling-symbolic-link" {
				if err := os.WriteFile(aliases, original, 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := aliases
			if mode != "same-path" {
				config = filepath.Join(root, "settings.json")
				link := os.Symlink
				if mode == "hard-link" {
					link = os.Link
				}
				if err := link(aliases, config); err != nil {
					t.Skipf("link unavailable: %v", err)
				}
			}
			code, out, diagnostics := runCLI(t, config, "config", "delete", "aa")
			if code != 1 || out != "" || !strings.Contains(diagnostics, "overlap") {
				t.Errorf("overlapping storage accepted: code=%d out=%q stderr=%q", code, out, diagnostics)
			}
			data, err := os.ReadFile(aliases)
			if mode == "dangling-symbolic-link" {
				if !os.IsNotExist(err) {
					t.Errorf("dangling alias storage was initialized: data=%q err=%v", data, err)
				}
			} else if err != nil || !bytes.Equal(data, original) {
				t.Errorf("alias storage changed: data=%q err=%v", data, err)
			}
		})
	}
}

func TestReservedConfigPathCannotInitializeAliasFile(t *testing.T) {
	names := []string{"rc_remotes.json"}
	if runtime.GOOS == "windows" {
		names = append(names, "RC_REMOTES.JSON.")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			code, out, diagnostics := runCLI(t, filepath.Join(root, name), "listremotes")
			if code != 1 || out != "" || !strings.Contains(diagnostics, "overlap") {
				t.Errorf("reserved config path accepted: code=%d out=%q stderr=%q", code, out, diagnostics)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Errorf("reserved config initialized storage: entries=%v err=%v", entries, err)
			}
		})
	}
}

func TestSeparateConfigSymbolicLinkStillWorks(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual.json")
	config := filepath.Join(root, "settings.json")
	if err := os.WriteFile(actual, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, config); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	aliases := filepath.Join(root, "rc_remotes.json")
	if err := os.WriteFile(aliases, []byte(`{"aa":7,"bb":8}`), 0600); err != nil {
		t.Fatal(err)
	}
	mustRun(t, config, "config", "rename", "aa", "cc")
	if data, err := os.ReadFile(actual); err != nil || string(data) != "{}" {
		t.Fatalf("unrelated account file changed: data=%q err=%v", data, err)
	}
	if info, err := os.Lstat(config); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config symbolic link changed: info=%v err=%v", info, err)
	}
	code, out, diagnostics := runCLI(t, config, "listremotes")
	if code != 0 || out != "bb:\ncc:\n" {
		t.Fatalf("separate storage failed: code=%d out=%q stderr=%q", code, out, diagnostics)
	}
}
