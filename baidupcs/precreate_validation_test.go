package baidupcs

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestUploadPrecreateRequiresSupportedModeAndUsableSession(t *testing.T) {
	for _, tc := range []struct {
		name, response, existing string
		fake, valid              bool
	}{
		{name: "missing-mode", response: `{"errno":0,"uploadid":"fixture"}`},
		{name: "unknown-mode", response: `{"errno":0,"return_type":9,"uploadid":"fixture"}`},
		{name: "missing-session", response: `{"errno":0,"return_type":1}`},
		{name: "blank-session", response: `{"errno":0,"return_type":1,"uploadid":"  "}`},
		{name: "fresh", response: `{"errno":0,"return_type":1,"uploadid":"fixture"}`, valid: true},
		{name: "resumed", response: `{"errno":0,"return_type":1}`, existing: "existing-session", valid: true},
		{name: "rapid-hit", response: `{"errno":0,"return_type":2}`, valid: true},
		{name: "fake-missing-session", response: `{"errno":0,"return_type":1}`, fake: true},
		{name: "fake-rapid-hit", response: `{"errno":0,"return_type":2}`, fake: true},
		{name: "fake-fresh", response: `{"errno":0,"return_type":1,"uploadid":"fixture"}`, fake: true, valid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("method") == "meta" {
					return response(r, `{"error_code":31066}`), nil
				}
				return response(r, tc.response), nil
			})
			var err pcserror.Error
			var info uploadPrecreateJSON
			if tc.fake {
				err, info = pcs.FakeRapidUploadWithContext(context.Background(), "/fixture", OverWritePolicy, 8)
			} else {
				err, info = pcs.RapidUploadWithContext(context.Background(), "/fixture", OverWritePolicy, tc.existing, EmptyContentMD5, EmptyContentMD5, "", "0", 0, 8, 8, 1, []string{EmptyContentMD5})
			}
			if (err == nil) != tc.valid {
				t.Fatalf("precreate acceptance: valid=%v err=%v", tc.valid, err)
			}
			if !tc.valid && (err.GetErrType() != pcserror.ErrTypeInternalError || !errors.Is(err.GetError(), ErrUploadPrecreateInvalid)) {
				t.Fatalf("invalid precreate response misclassified: %v", err)
			}
			if tc.valid && tc.existing != "" && info.UploadID != tc.existing {
				t.Fatalf("existing upload session was lost: %+v", info)
			}
		})
	}
}
