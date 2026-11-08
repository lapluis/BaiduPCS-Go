package pcsclone

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestCookieLoginCommandsUseExactCookieFields(t *testing.T) {
	for _, operation := range []string{"create", "reconnect"} {
		for _, tc := range []struct {
			name, cookies string
			valid         bool
		}{
			{"terminated", "BDUSS=SYNTHETIC;", true},
			{"unterminated", "BDUSS=SYNTHETIC", true},
			{"last-cookie", "OTHER=ok; BDUSS=SYNTHETIC", true},
			{"quoted-value", `BDUSS="SYNTHETIC"`, true},
			{"missing", "OTHER=SYNTHETIC;", false},
			{"wrong-name", "NOTBDUSS=SYNTHETIC;", false},
			{"embedded-value", "OTHER=BDUSS=SYNTHETIC;", false},
			{"empty", "BDUSS=;", false},
			{"empty-first-duplicate", "BDUSS=; BDUSS=SYNTHETIC;", false},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				c, out := commandFixture(t, func(http.ResponseWriter, *http.Request) {
					t.Error("synthetic offline login reached the network")
				}, buildConfigGroup().Group, operation, []string{"bd"}, []string{"--cookies", tc.cookies})
				cfg := pcsconfig.Config
				cfg.ForceLogin = "offline fixture"
				cfg.BaiduActiveUID = 77
				cfg.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 77, Name: "other"}, BDUSS: "SYNTHETIC-OTHER"}}
				c.app.Resolver.Aliases = map[string]uint64{"other": 77}
				if operation == "reconnect" {
					cfg.BaiduUserList = append(cfg.BaiduUserList, &pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 24, Name: "original"}, BDUSS: "SYNTHETIC-OLD"})
					cfg.BaiduActiveUID = 24
					c.app.Resolver.Aliases["bd"] = 24
				}
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				if err := c.app.Resolver.Save(); err != nil {
					t.Fatal(err)
				}
				read := func(path string) []byte {
					t.Helper()
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					return data
				}
				aliasesPath := filepath.Join(filepath.Dir(c.app.ConfigFile), "rc_remotes.json")
				before, aliases := read(c.app.ConfigFile), read(aliasesPath)
				err := c.cmd.Run(c)
				after := read(c.app.ConfigFile)
				if !tc.valid {
					var exit *exitError
					if !errors.As(err, &exit) || exit.code != 2 || out.Len() != 0 {
						t.Fatalf("invalid cookie did not fail as usage error: err=%v output=%q", err, out.String())
					}
					if !bytes.Equal(before, after) || !bytes.Equal(aliases, read(aliasesPath)) {
						t.Fatal("invalid cookie changed persisted configuration")
					}
					if strings.Contains(err.Error(), "SYNTHETIC") {
						t.Fatal("cookie diagnostic exposed its value")
					}
					return
				}
				if err != nil {
					t.Fatal("valid cookie rejected:", err)
				}
				var saved legacyConfig
				if err := json.Unmarshal(after, &saved); err != nil {
					t.Fatal(err)
				}
				var loggedIn *pcsconfig.Baidu
				for _, user := range saved.BaiduUserList {
					if user != nil && user.UID == 24 {
						loggedIn = user
					}
				}
				if loggedIn == nil || loggedIn.BDUSS != "SYNTHETIC" || loggedIn.COOKIES != tc.cookies || c.app.Resolver.Aliases["bd"] != 24 || len(saved.BaiduUserList) != 2 {
					t.Fatal("valid login did not persist the exact cookie and account identity")
				}
				if other := findBaiduUser(77); other == nil || other.BDUSS != "SYNTHETIC-OTHER" {
					t.Fatal("login changed the unrelated account")
				}
				if out.Len() == 0 || strings.Contains(out.String(), "SYNTHETIC") {
					t.Fatalf("login output missing or contains credentials: %q", out.String())
				}
				if operation == "reconnect" && !bytes.Equal(aliases, read(aliasesPath)) {
					t.Fatal("reconnect changed saved aliases")
				}
			})
		}
	}
}
