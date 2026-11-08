package requester

import (
	"net/http"
	"net/url"
	"testing"
)

func TestProxyHostnameRulesMatchCompleteNames(t *testing.T) {
	previousProxy, previousRules := ProxyAddr, ProxyHostnameRules
	t.Cleanup(func() { SetGlobalProxy(previousProxy); SetProxyHostnameRules(previousRules) })
	const explicit = "http://explicit-proxy.fixture.invalid:9443"
	SetGlobalProxy(explicit)
	for _, tc := range []struct {
		name, rules, target string
		match               bool
	}{
		{"all-hosts", "", "https://other.fixture.invalid/file", true},
		{"first-host", "alpha.fixture.invalid,beta.fixture.invalid", "https://alpha.fixture.invalid/file", true},
		{"second-host", "alpha.fixture.invalid,beta.fixture.invalid", "https://beta.fixture.invalid/file", true},
		{"upper-rule", "ALPHA.FIXTURE.INVALID", "https://alpha.fixture.invalid/file", true},
		{"upper-host", "alpha.fixture.invalid", "https://ALPHA.FIXTURE.INVALID/file", true},
		{"spaced-list", " , alpha.fixture.invalid , , beta.fixture.invalid , ", "https://beta.fixture.invalid:8443/file", true},
		{"parent-substring", "alpha.fixture.invalid,beta.fixture.invalid", "https://fixture.invalid/file", false},
		{"suffix-substring", "alpha.fixture.invalid", "https://pha.fixture.invalid/file", false},
		{"prefix-substring", "alpha.fixture.invalid", "https://alpha/file", false},
		{"unlisted-child", "alpha.fixture.invalid", "https://child.alpha.fixture.invalid/file", false},
		{"host-with-suffix", "alpha.fixture.invalid", "https://alpha.fixture.invalid.other/file", false},
		{"unlisted-host", "alpha.fixture.invalid", "https://other.fixture.invalid/file", false},
		{"empty-items", " , , ", "https://other.fixture.invalid/file", false},
		{"ipv4", "192.0.2.1,192.0.2.10", "https://192.0.2.1/file", true},
		{"ipv4-substring", "192.0.2.10", "https://192.0.2.1/file", false},
		{"ipv6", "2001:db8::1,2001:db8::2", "https://[2001:db8::1]:8443/file", true},
		{"ipv6-substring", "2001:db8::10", "https://[2001:db8::1]/file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetProxyHostnameRules(tc.rules)
			req, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			want, wantErr := http.ProxyFromEnvironment(req)
			if tc.match {
				want, wantErr = url.Parse(explicit)
			}
			got, err := proxyFunc(req)
			if (err == nil) != (wantErr == nil) || !sameProxyURL(got, want) {
				// Do not print an environment proxy URL: it may contain credentials.
				t.Fatalf("wrong proxy route for %s: explicit match wanted=%t", req.URL.Hostname(), tc.match)
			}
		})
	}
}

func sameProxyURL(a, b *url.URL) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.String() == b.String()
}
