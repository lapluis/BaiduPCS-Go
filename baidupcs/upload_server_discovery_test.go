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
		{"legacy", `{"error_code":0,"server":["legacy-fixture.invalid"]}`, "legacy-fixture.invalid", false},
		{"wrong-type", `{"error_code":0,"servers":[7]}`, "", true},
		{"remote-error", `{"error_code":1,"servers":[null]}`, "", true},
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
			})
		}
	}
}
