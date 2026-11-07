package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestUploadServerDiscoveryHandlesNullEntries(t *testing.T) {
	for _, tc := range []struct {
		name, payload, host string
		rejected            bool
	}{
		{"null-only", `{"error_code":0,"servers":[null]}`, "", false},
		{"mixed", `{"error_code":0,"servers":[null,{"server":"https://upload-fixture.invalid/path"},null]}`, "upload-fixture.invalid", false},
		{"legacy-fallback", `{"error_code":0,"servers":[null],"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"empty-host-fallback", `{"error_code":0,"servers":[{"server":"not-a-url"}],"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"valid", `{"error_code":0,"servers":[{"server":"https://upload-fixture.invalid/path"}]}`, "upload-fixture.invalid", false},
		{"valid-without-hyphen", `{"error_code":0,"servers":[{"server":"https://upload.fixture.invalid/path"}]}`, "upload.fixture.invalid", false},
		{"modern-port", `{"error_code":0,"servers":[{"server":"https://upload.fixture.invalid:8443/path"}]}`, "upload.fixture.invalid:8443", false},
		{"modern-ipv6", `{"error_code":0,"servers":[{"server":"https://[2001:db8::1]:8443/path"}]}`, "[2001:db8::1]:8443", false},
		{"modern-bad-port", `{"error_code":0,"servers":[{"server":"https://upload.fixture.invalid:65536/path"}]}`, "", false},
		{"legacy-bad-ipv6", `{"error_code":0,"server":["[:]"]}`, "", false},
		{"unsupported-scheme", `{"error_code":0,"servers":[{"server":"ftp://upload-fixture.invalid/path"}]}`, "", false},
		{"userinfo", `{"error_code":0,"servers":[{"server":"https://fixture-user:fixture-password@upload-fixture.invalid/path"}]}`, "", false},
		{"invalid-modern-legacy-fallback", `{"error_code":0,"servers":[{"server":"ftp://upload-fixture.invalid/path"}],"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"scheme-relative", `{"error_code":0,"servers":[{"server":"//upload.fixture.invalid/path"}]}`, "upload.fixture.invalid", false},
		{"legacy", `{"error_code":0,"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"legacy-space", `{"error_code":0,"server":["not a host"]}`, "", false},
		{"legacy-path", `{"error_code":0,"server":["legacy-fixture.invalid/path"]}`, "", false},
		{"legacy-url", `{"error_code":0,"server":["https://legacy-fixture.invalid/path"]}`, "", false},
		{"legacy-userinfo", `{"error_code":0,"server":["fixture-user@legacy-fixture.invalid"]}`, "", false},
		{"legacy-query", `{"error_code":0,"server":["legacy-fixture.invalid?query=fixture"]}`, "", false},
		{"legacy-fragment", `{"error_code":0,"server":["legacy-fixture.invalid#fixture"]}`, "", false},
		{"legacy-mixed", `{"error_code":0,"server":["","not a host","legacy-fixture.invalid/path","legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"legacy-port", `{"error_code":0,"server":["legacy-fixture.invalid:8443"]}`, "legacy-fixture.invalid:8443", false},
		{"legacy-ipv6", `{"error_code":0,"server":["[2001:db8::1]:8443"]}`, "[2001:db8::1]:8443", false},
		{"legacy-bad-port", `{"error_code":0,"server":["legacy-fixture.invalid:65536"]}`, "", false},
		{"legacy-zero-port", `{"error_code":0,"server":["legacy-fixture.invalid:0"]}`, "", false},
		{"legacy-unbracketed-ipv6", `{"error_code":0,"server":["2001:db8::1"]}`, "", false},
		{"wrong-type", `{"error_code":0,"servers":[7]}`, "", true},
		{"remote-error", `{"error_code":1,"servers":[null]}`, "", true},
		{"alternate-error", `{"errno":-9,"servers":[{"server":"https://upload-fixture.invalid/path"}]}`, "", true},
		{"alternate-error-with-zero", `{"error_code":0,"errno":-9,"servers":[{"server":"https://upload-fixture.invalid/path"}]}`, "", true},
	} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", tc.name, contextual), func(t *testing.T) {
				body := &uploadDigestBody{Reader: strings.NewReader(tc.payload)}
				client := requester.NewHTTPClient()
				calls := 0
				client.Client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
				})
				pcs := NewPCSWithClient(1, client)
				var got string
				var err pcserror.Error
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Errorf("discovery panicked on server list: %v", p)
						}
					}()
					if contextual {
						err, got = pcs.GetRandomPCSHostWithContext(context.Background())
					} else {
						err, got = pcs.GetRandomPCSHost()
					}
				}()
				if (err != nil) != tc.rejected || got != tc.host || calls != 1 || !body.closed {
					t.Fatalf("server discovery: host=%q want=%q err=%v calls=%d closed=%v", got, tc.host, err, calls, body.closed)
				}
				if strings.HasPrefix(tc.name, "alternate-error") && (err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != -9) {
					t.Fatalf("alternate discovery error lost: %v", err)
				}
			})
		}
	}
}
