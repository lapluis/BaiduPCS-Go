package pcsclone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRenamePreservesAliasStorageLink(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "config")
	shared := filepath.Join(root, "shared")
	for _, path := range []string{dir, shared} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(dir, defaultConfigName)
	if err := os.WriteFile(config, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(shared, "remotes.json")
	if err := os.WriteFile(target, []byte(`{"old":7}`), 0600); err != nil {
		t.Fatal(err)
	}
	aliases := filepath.Join(dir, "rc_remotes.json")
	link := filepath.Join("..", "shared", "remotes.json")
	if err := os.Symlink(link, aliases); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	code, out, diagnostic := runCLI(t, config, "config", "rename", "old", "new")
	if code != 0 {
		t.Fatalf("rename failed: code=%d output=%q stderr=%q", code, out, diagnostic)
	}
	if info, err := os.Lstat(aliases); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("rename replaced alias link: %v", err)
	}
	if got, err := os.Readlink(aliases); err != nil || got != link {
		t.Fatalf("link target changed: %q, %v", got, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]uint64
	if err := json.Unmarshal(data, &saved); err != nil || len(saved) != 1 || saved["new"] != 7 {
		t.Fatalf("physical alias target not updated: %q, %v", data, err)
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != "{}" {
		t.Fatalf("account configuration changed: %q, %v", data, err)
	}
	code, out, diagnostic = runCLI(t, config, "listremotes")
	if code != 0 || out != "new:\n" {
		t.Fatalf("renamed linked alias did not reload: code=%d output=%q stderr=%q", code, out, diagnostic)
	}
}
