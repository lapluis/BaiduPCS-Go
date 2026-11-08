package baidupcs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestCloudDlWriteRejectsInvalidSuccessValues(t *testing.T) {
	for _, tc := range []struct {
		name, action, payload string
		want                  int64
		invalid               bool
	}{
		{"add-positive", "add", `{"error_code":0,"task_id":7}`, 7, false},
		{"add-large-id", "add", `{"error_code":0,"task_id":9223372036854775807}`, 9223372036854775807, false},
		{"add-zero", "add", `{"error_code":0,"task_id":0}`, 0, true},
		{"add-negative", "add", `{"error_code":0,"task_id":-1}`, 0, true},
		{"add-missing", "add", `{"error_code":0}`, 0, true},
		{"add-null", "add", `{"error_code":0,"task_id":null}`, 0, true},
		{"clear-empty", "clear", `{"error_code":0,"total":0}`, 0, false},
		{"clear-positive", "clear", `{"error_code":0,"total":3}`, 3, false},
		{"clear-negative", "clear", `{"error_code":0,"total":-1}`, 0, true},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/context", true: "/legacy"}[legacy], func(t *testing.T) {
				body := &locateBody{Reader: strings.NewReader(tc.payload)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				value, err := invokeCloudWrite(pcs, context.Background(), tc.action, legacy)
				if !body.closed {
					t.Error("response not closed")
				}
				if tc.invalid {
					var pcsErr pcserror.Error
					if err == nil || value != 0 || !errors.As(err, &pcsErr) || pcsErr.GetErrType() != pcserror.ErrTypeOthers {
						t.Fatalf("invalid success accepted: value=%d err=%v", value, err)
					}
				} else if err != nil || value != tc.want {
					t.Fatalf("valid result changed: value=%d want=%d err=%v", value, tc.want, err)
				}
			})
		}
	}
}
