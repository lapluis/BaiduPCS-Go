package pcsclone

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportCookieCredentials(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy", defaultConfigName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	fixture := `{"baidu_user_list":[
	{"uid":1,"name":"cookie-only","cookies":"BDUSS=SYNTHETIC_ONE"},
	{"uid":2,"name":"explicit","bduss":"SYNTHETIC_TWO","cookies":"BDUSS=OTHER"},
	{"uid":3,"name":"wrong-name","cookies":"NOTBDUSS=SYNTHETIC_BAD"},
	{"uid":4,"name":"empty","cookies":"BDUSS=;"},
	{"uid":5,"name":"embedded","cookies":"OTHER=BDUSS=SYNTHETIC_BAD"},
	{"uid":6,"name":"cookie-last","cookies":"OTHER=ok; BDUSS=SYNTHETIC_SIX"}
	]}`
	if err := os.WriteFile(legacy, []byte(fixture), 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "destination", defaultConfigName)
	out := mustRun(t, config, "config", "import-pcsgo", "--from", legacy, "--json")
	if strings.Contains(out, "SYNTHETIC") {
		t.Fatal("import output disclosed credentials")
	}
	var summary struct{ Imported, Skipped int }
	if err := json.Unmarshal([]byte(out), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Imported != 3 || summary.Skipped != 3 {
		t.Errorf("wrong import counts: %+v", summary)
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var stored legacyConfig
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	want := map[uint64]string{1: "SYNTHETIC_ONE", 2: "SYNTHETIC_TWO", 6: "SYNTHETIC_SIX"}
	if len(stored.BaiduUserList) != len(want) {
		t.Error("unexpected persisted account count")
	}
	for _, user := range stored.BaiduUserList {
		if expected, ok := want[user.UID]; !ok || user.BDUSS != expected {
			t.Errorf("account %d has invalid or missing imported credential", user.UID)
		}
		if user.BaiduPCS().GetBDUSS() != want[user.UID] {
			t.Errorf("account %d client did not use imported credential", user.UID)
		}
	}
	after, err := os.ReadFile(legacy)
	if err != nil || string(after) != fixture {
		t.Fatal("import changed source")
	}
}
