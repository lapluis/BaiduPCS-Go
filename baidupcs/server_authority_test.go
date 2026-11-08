package baidupcs

import (
	"net/url"
	"testing"
)

func TestServerAuthorityValidatesIPv6IndependentlyOfURLParser(t *testing.T) {
	for _, tc := range []struct {
		host  string
		valid bool
	}{
		{"[:]", false}, {"[]", false}, {"[not-an-ip]", false},
		{"[127.0.0.1]", false}, {"[2001:db8:::1]", false}, {"[::1%]", false},
		{"2001:db8::1", false}, {"[::1]:0", false}, {"[::1]:65536", false},
		{"[::]", true}, {"[::1]", true}, {"[2001:db8::1]:8443", true},
		{"[fe80::1%fixture]:8443", true}, {"[::ffff:192.0.2.1]", true},
		{"upload.fixture.invalid:8443", true}, {"127.0.0.1:80", true},
	} {
		t.Run(tc.host, func(t *testing.T) {
			// Construct the parsed URL directly: newer net/url releases reject
			// malformed literals before this shared validator is reached.
			if got := validServerAuthority(&url.URL{Scheme: "https", Host: tc.host}); got != tc.valid {
				t.Fatalf("validServerAuthority(%q)=%v, want %v", tc.host, got, tc.valid)
			}
		})
	}
}
