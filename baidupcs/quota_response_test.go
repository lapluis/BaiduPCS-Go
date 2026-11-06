package baidupcs

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestQuotaRejectsMissingOrNullFields(t *testing.T) {
	for _, payload := range []string{`{}`, `{"error_code":0}`, `{"quota":100}`, `{"used":25}`, `{"quota":null,"used":25}`, `{"quota":100,"used":null}`} {
		t.Run(payload, func(t *testing.T) {
			body := &shareTrackedBody{Reader: strings.NewReader(payload)}
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				resp := response(r, "")
				resp.Body = body
				return resp, nil
			})
			total, used, err := pcs.QuotaInfo()
			if err == nil || total != 0 || used != 0 {
				t.Fatalf("incomplete quota accepted: total=%d used=%d err=%v", total, used, err)
			}
			if !body.closed {
				t.Fatal("invalid quota response not closed")
			}
		})
	}
}

func TestQuotaResponsesAndOwnership(t *testing.T) {
	cases := []struct {
		name, payload string
		total, used   int64
		failure       bool
	}{{"zero", `{"quota":0,"used":0}`, 0, 0, false}, {"normal", `{"quota":100,"used":25}`, 100, 25, false}, {"large", `{"quota":9007199254740993,"used":25}`, 9007199254740993, 25, false}, {"remote", `{"error_code":31066}`, 0, 0, true}, {"invalid-json", `{`, 0, 0, true}}
	for _, tc := range cases {
		for _, legacy := range []bool{false, true} {
			name := tc.name
			if legacy {
				name += "/legacy"
			}
			t.Run(name, func(t *testing.T) {
				body := &shareTrackedBody{Reader: strings.NewReader(tc.payload)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					if r.URL.Query().Get("method") != "info" || r.Method != http.MethodGet {
						t.Errorf("quota endpoint changed: %s %s", r.Method, r.URL)
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var total, used int64
				var err pcserror.Error
				if legacy {
					total, used, err = pcs.QuotaInfo()
				} else {
					total, used, err = pcs.QuotaInfoWithContext(context.Background())
				}
				if !body.closed {
					t.Fatal("quota response not closed")
				}
				if tc.failure {
					if err == nil || total != 0 || used != 0 {
						t.Fatalf("invalid quota accepted: total=%d used=%d err=%v", total, used, err)
					}
					if tc.name == "remote" && err.GetRemoteErrCode() != 31066 {
						t.Fatalf("remote error lost: %v", err)
					}
					return
				}
				if err != nil || total != tc.total || used != tc.used {
					t.Fatalf("quota changed: total=%d used=%d err=%v", total, used, err)
				}
			})
		}
	}
}
