package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestUploadTargetCheckOnlyIgnoresMissingFileErrors(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		code          int
		missing       bool
	}{
		{"pcs-error", `{"error_code":31064,"error_msg":"fixture denied"}`, 31064, false},
		{"missing-user", `{"error_code":31045}`, 31045, false},
		{"pan-error", `{"errno":-6,"errmsg":"fixture login required"}`, -6, false},
		{"unknown-error", `{"error_code":99999}`, 99999, false},
		{"zero-native-error", `{"error_code":0,"errno":-6}`, -6, false},
		{"pcs-missing", `{"error_code":31066}`, 31066, true},
		{"pan-missing", `{"errno":-9}`, -9, true},
		{"zero-native-missing", `{"error_code":0,"errno":-9}`, -9, true},
	} {
		for _, fake := range []bool{false, true} {
			for _, contextual := range []bool{false, true} {
				for _, policy := range []string{SkipPolicy, RsyncPolicy, OverWritePolicy} {
					t.Run(fmt.Sprintf("%s/fake=%t/context=%t/%s", tc.name, fake, contextual, policy), func(t *testing.T) {
						metas, creates := 0, 0
						var bodies []*locateBody
						pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
							payload := tc.payload
							if r.URL.Query().Get("method") == "meta" {
								metas++
							} else {
								creates++
								if r.URL.Path != "/api/precreate" {
									t.Error("unexpected request", r.URL.Path)
								}
								payload = `{"errno":0,"return_type":1,"uploadid":"fixture-session"}`
							}
							body := &locateBody{Reader: strings.NewReader(payload)}
							bodies = append(bodies, body)
							resp := response(r, "")
							resp.Body = body
							return resp, nil
						})
						var err pcserror.Error
						if fake {
							if contextual {
								err, _ = pcs.FakeRapidUploadWithContext(context.Background(), "/fixture", policy, 8)
							} else {
								err, _ = pcs.FakeRapidUpload("/fixture", policy, 8)
							}
						} else if contextual {
							err, _ = pcs.RapidUploadWithContext(context.Background(), "/fixture", policy, "session", EmptyContentMD5, EmptyContentMD5, "", "0", 0, 8, 8, 1, []string{EmptyContentMD5})
						} else {
							err, _ = pcs.RapidUpload("/fixture", policy, "session", EmptyContentMD5, EmptyContentMD5, "", "0", 0, 8, 8, 1, []string{EmptyContentMD5})
						}
						if tc.missing {
							if err != nil || metas != 1 || creates != 1 {
								t.Fatalf("missing target did not reach precreation: metas=%d creates=%d err=%v", metas, creates, err)
							}
						} else if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code || metas != 1 || creates != 0 {
							t.Fatalf("target query failure bypassed safety check: metas=%d creates=%d err=%v", metas, creates, err)
						}
						for _, body := range bodies {
							if !body.closed {
								t.Error("response body not closed")
							}
						}
					})
				}
			}
		}
	}
}
