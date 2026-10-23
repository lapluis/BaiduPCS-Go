package pcsconfig

import (
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"testing"
)

func TestCookieLogin(t *testing.T) {
	for _, tc := range []struct {
		name, bduss, cookies, want string
	}{
		{"missing", "", "STOKEN=SYNTHETIC;", ""},
		{"empty", "", "BDUSS=;", ""},
		{"wrong name", "", "NOTBDUSS=SYNTHETIC;", ""},
		{"last cookie", "", "STOKEN=OTHER; BDUSS=SYNTHETIC", "SYNTHETIC"},
		{"terminated", "", "BDUSS=SYNTHETIC; STOKEN=OTHER;", "SYNTHETIC"},
		{"explicit wins", "EXPLICIT", "BDUSS=OTHER;", "EXPLICIT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() != nil {
					t.Fatal("cookie login panicked")
				}
			}()
			c := reloadFixture(t)
			c.InitDefaultConfig()
			c.ForceLogin = "fixture"
			user, err := c.SetupUserByBDUSS(tc.bduss, "", "", tc.cookies)
			if tc.want == "" {
				if err == nil || user != nil || len(c.BaiduUserList) != 0 {
					t.Fatal("invalid cookie modified account state")
				}
				return
			}
			if err != nil || user == nil || user.BDUSS != tc.want {
				t.Fatal("valid cookie was not parsed correctly")
			}
		})
	}
}

func TestDefaultConfigRestoresUserAgent(t *testing.T) {
	c := reloadFixture(t)
	c.SetUserAgent("fixture-custom")
	c.InitDefaultConfig()
	if c.UserAgent != requester.DefaultUserAgent {
		t.Fatal("default config retained custom user agent")
	}
}
