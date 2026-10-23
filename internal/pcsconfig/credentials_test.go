package pcsconfig

import (
	"net/url"
	"testing"
)

func TestClientCredentialPrecedence(t *testing.T) {
	c := reloadFixture(t)
	c.InitDefaultConfig()
	for _, tc := range []struct {
		name string
		user Baidu
		want map[string]string
	}{
		{"cookie fallback", Baidu{COOKIES: "BDUSS=COOKIE_B; STOKEN=COOKIE_S; SBOXTKN=COOKIE_X; BAIDUID=COOKIE_ID"}, map[string]string{"BDUSS": "COOKIE_B", "STOKEN": "COOKIE_S", "SBOXTKN": "COOKIE_X", "BAIDUID": "COOKIE_ID"}},
		{"explicit overrides", Baidu{BDUSS: "EXPLICIT_B", STOKEN: "EXPLICIT_S", SBOXTKN: "EXPLICIT_X", COOKIES: "BDUSS=COOKIE_B; STOKEN=COOKIE_S; SBOXTKN=COOKIE_X; BAIDUID=COOKIE_ID"}, map[string]string{"BDUSS": "EXPLICIT_B", "STOKEN": "EXPLICIT_S", "SBOXTKN": "EXPLICIT_X", "BAIDUID": "COOKIE_ID"}},
		{"fallback stoken preserves explicit", Baidu{BDUSS: "EXPLICIT_B", SBOXTKN: "EXPLICIT_X", COOKIES: "STOKEN=COOKIE_S"}, map[string]string{"BDUSS": "EXPLICIT_B", "STOKEN": "COOKIE_S", "SBOXTKN": "EXPLICIT_X"}},
		{"bduss only cookie", Baidu{COOKIES: "BDUSS=COOKIE_B"}, map[string]string{"BDUSS": "COOKIE_B"}},
		{"unrelated cookie name", Baidu{BDUSS: "EXPLICIT_B", COOKIES: "NOTSTOKEN=OTHER"}, map[string]string{"BDUSS": "EXPLICIT_B", "STOKEN": ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := tc.user.BaiduPCS().GetClient()
			got := map[string]string{}
			for _, cookie := range client.Jar.Cookies(&url.URL{Scheme: "https", Host: "baidu.com", Path: "/"}) {
				got[cookie.Name] = cookie.Value
			}
			for name, want := range tc.want {
				if got[name] != want {
					t.Errorf("%s did not respect credential precedence", name)
				}
			}
		})
	}
}
