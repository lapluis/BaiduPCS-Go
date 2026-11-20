package pcsclone

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestReconnectKeepsAccountIdentityAndPersistedAliases(t *testing.T) {
	for _, mode := range []string{"same-uid", "wrong-uid", "unknown-alias"} {
		t.Run(mode, func(t *testing.T) {
			c, out := commandFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Error("offline reconnect reached the network")
			}, buildConfigGroup().Group, "reconnect", []string{"bd"}, []string{"--bduss", "SYNTHETIC-NEW", "--stoken", "SYNTHETIC-STOKEN"})
			cfg := c.app.Config
			// The existing offline login mode constructs UID 24 without any
			// authentication request. All account data here is synthetic.
			cfg.ForceLogin = "offline fixture"
			uid := uint64(7)
			if mode == "same-uid" {
				uid = 24
			}
			cfg.BaiduActiveUID = uid
			cfg.BaiduUserList = pcsconfig.BaiduUserList{
				&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: uid, Name: "original"}, BDUSS: "SYNTHETIC-OLD", Workdir: "/original"},
				&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 77, Name: "unrelated"}, BDUSS: "SYNTHETIC-OTHER", Workdir: "/other"},
			}
			if mode == "wrong-uid" {
				cfg.BaiduUserList = append(cfg.BaiduUserList, &pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 24, Name: "also retained"}, BDUSS: "SYNTHETIC-EXISTING-24"})
			}
			c.app.Resolver.Aliases = map[string]uint64{"bd": uid, "other": 77}
			if mode == "unknown-alias" {
				c.args[0] = "missing"
			}
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			if err := c.app.Resolver.Save(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(c.app.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			aliasPath := filepath.Join(filepath.Dir(c.app.ConfigFile), "rc_remotes.json")
			aliases, err := os.ReadFile(aliasPath)
			if err != nil {
				t.Fatal(err)
			}
			runErr := c.cmd.Run(c)
			after, err := os.ReadFile(c.app.ConfigFile)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "same-uid" {
				if runErr == nil || out.Len() != 0 || !bytes.Equal(before, after) {
					t.Fatalf("rejected reconnect changed persisted state: err=%v output=%q", runErr, out.String())
				}
			} else {
				user, other := findBaiduUser(cfg, uid), findBaiduUser(cfg, 77)
				if runErr != nil || user == nil || user.BDUSS != "SYNTHETIC-NEW" || user.STOKEN != "SYNTHETIC-STOKEN" || other == nil || other.BDUSS != "SYNTHETIC-OTHER" || other.Workdir != "/other" || len(cfg.BaiduUserList) != 2 || bytes.Equal(before, after) {
					t.Fatalf("same-account reconnect did not preserve account boundaries: %v", runErr)
				}
				if !strings.Contains(out.String(), "Reconnected remote") || strings.Contains(out.String(), "SYNTHETIC-") {
					t.Fatalf("reconnect output missing or exposed synthetic credentials: %q", out.String())
				}
			}
			memory, err := json.Marshal(cfg.BaiduUserList)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Users     json.RawMessage `json:"baidu_user_list"`
				ActiveUID uint64          `json:"baidu_active_uid"`
			}
			if err := json.Unmarshal(after, &saved); err != nil {
				t.Fatal(err)
			}
			var compact bytes.Buffer
			if err := json.Compact(&compact, saved.Users); err != nil || !bytes.Equal(memory, compact.Bytes()) || saved.ActiveUID != uid || cfg.BaiduActiveUID != uid {
				t.Fatalf("reconnect left persisted and in-memory accounts inconsistent: %v", err)
			}
			if current, err := os.ReadFile(aliasPath); err != nil || !bytes.Equal(current, aliases) {
				t.Fatalf("reconnect changed persisted aliases: %v", err)
			}
		})
	}
}
