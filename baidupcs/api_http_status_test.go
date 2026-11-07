package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestAPIMutationsRejectHTTPFailureWithSuccessBody(t *testing.T) {
	for _, operation := range []string{"copy", "move", "remove", "mkdir", "merge"} {
		for _, tc := range []struct {
			name   string
			code   int
			body   string
			remote int
		}{
			{"server-success-json", 500, `{"error_code":0,"errno":0}`, 0},
			{"server-empty-object", 502, `{}`, 0},
			{"server-html", 503, `<html>fixture failure</html>`, 0},
			{"redirect", 307, `{"error_code":0,"errno":0}`, 0},
			{"pcs-error", 403, `{"error_code":31066}`, 31066},
			{"pan-error", 403, `{"errno":-9}`, -9},
			{"success", 200, `{"error_code":0,"errno":0}`, 0},
		} {
			t.Run(operation+"/"+tc.name, func(t *testing.T) {
				body := &uploadDigestBody{Reader: strings.NewReader(tc.body)}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := response(req, "")
					resp.StatusCode, resp.Body = tc.code, body
					return resp, nil
				})
				var err pcserror.Error
				switch operation {
				case "copy":
					err = pcs.Copy(&CpMvJSON{From: "/one", To: "/two"})
				case "move":
					err = pcs.Move(&CpMvJSON{From: "/one", To: "/two"})
				case "remove":
					err = pcs.Remove("/one")
				case "mkdir":
					err = pcs.Mkdir("/one")
				case "merge":
					err = pcs.UploadCreateSuperFileWithContext(context.Background(), "fixture-session", OverWritePolicy, 8, "/one", map[int]string{0: strings.Repeat("a", 32)})
				}
				if calls != 1 || !body.closed {
					t.Errorf("response ownership/calls changed: calls=%d closed=%t", calls, body.closed)
				}
				if tc.code == 200 {
					if err != nil {
						t.Fatalf("normal success rejected: %v", err)
					}
				} else if tc.remote != 0 {
					if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != tc.remote {
						t.Errorf("remote error lost: %v", err)
					}
				} else if err == nil || err.GetErrType() != pcserror.ErrTypeNetError || !strings.Contains(err.Error(), fmt.Sprintf("HTTP status %d", tc.code)) {
					t.Errorf("HTTP failure accepted or status lost: %v", err)
				}
			})
		}
	}
}
