package pcsclone

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func aliasDeleteFixture(t *testing.T, aliases string) (string, string) {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(root, defaultConfigName)
	accounts := []byte(`{"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"name":"fixture-seven"},{"uid":8,"name":"fixture-eight"}]}`)
	aliasPath := filepath.Join(root, "rc_remotes.json")
	for path, data := range map[string][]byte{config: accounts, aliasPath: []byte(aliases)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return config, aliasPath
}

func TestAliasUpdateFailurePreservesAccountsAndAliases(t *testing.T) {
	for _, args := range [][]string{
		{"config", "rename", "old", "new"},
		{"config", "delete", "old"},
		{"config", "delete", "old", "--keep-account"},
	} {
		t.Run(args[1]+args[len(args)-1], func(t *testing.T) {
			config, aliases := aliasDeleteFixture(t, `{"old":7,"other":8}`)
			before := map[string][]byte{}
			for _, path := range []string{config, aliases} {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				before[path] = data
			}
			if err := os.Chmod(aliases, 0400); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(aliases, 0600) })
			if file, err := os.OpenFile(aliases, os.O_RDWR, 0600); err == nil {
				file.Close()
				t.Skip("effective user can write read-only files")
			}
			code, out, diagnostic := runCLI(t, config, args...)
			if code != 1 || out != "" || diagnostic == "" {
				t.Fatalf("save failure not reported: code=%d stdout=%q stderr=%q", code, out, diagnostic)
			}
			for path, original := range before {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, original) {
					t.Fatalf("failed command changed %s: %q, %v", filepath.Base(path), data, err)
				}
			}
			if got := mustRun(t, config, "listremotes"); got != "old:\nother:\n" {
				t.Fatalf("failed update changed reloaded aliases: %q", got)
			}
		})
	}
}

func TestConfigDeleteRetainsSharedAccounts(t *testing.T) {
	for _, tc := range []struct {
		name, aliases string
		keep          bool
		wantUID7      bool
		wantRemotes   string
	}{
		{"last-alias", `{"old":7,"other":8}`, false, false, "other:\n"},
		{"shared-account", `{"old":7,"shared":7,"other":8}`, false, true, "other:\nshared:\n"},
		{"keep-account", `{"old":7,"other":8}`, true, true, "other:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config, _ := aliasDeleteFixture(t, tc.aliases)
			args := []string{"config", "delete", "old"}
			if tc.keep {
				args = append(args, "--keep-account")
			}
			mustRun(t, config, args...)
			data, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Active uint64 `json:"baidu_active_uid"`
				Users  []struct {
					UID uint64 `json:"uid"`
				} `json:"baidu_user_list"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			users := map[uint64]bool{}
			for _, user := range saved.Users {
				users[user.UID] = true
			}
			wantCount, wantActive := 1, uint64(8)
			if tc.wantUID7 {
				wantCount, wantActive = 2, 7
			}
			if users[7] != tc.wantUID7 || !users[8] || len(saved.Users) != wantCount || saved.Active != wantActive {
				t.Fatalf("unexpected account retention or selection: %s", data)
			}
			if got := mustRun(t, config, "listremotes"); got != tc.wantRemotes {
				t.Fatalf("unexpected reloaded aliases: %q", got)
			}
		})
	}
}
