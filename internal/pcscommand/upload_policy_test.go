package pcscommand

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestUploadRejectsInvalidPolicyBeforeSideEffects(t *testing.T) {
	for _, policy := range []string{"skpi", "OVERWRITE", "skip ", "unexpected"} {
		for _, target := range []string{"/fixture", "/fixture*"} {
			t.Run(fmt.Sprintf("%q/%s", policy, target), func(t *testing.T) {
				exportFixture(t)
				pcsconfig.Config.UPolicy = baidupcs.OverWritePolicy
				configDir := t.TempDir()
				t.Setenv(pcsconfig.EnvConfigDir, configDir)
				requests := 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						req.Body.Close()
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errno":2,"errmsg":"fixture must not be queried"}`)), Request: req}, nil
				})
				options := &UploadOptions{Policy: policy}
				var got error
				captureExportCall(t, func() { got = RunUpload([]string{t.TempDir()}, target, options) })
				if got == nil || !strings.Contains(got.Error(), "上传同名文件策略") {
					t.Errorf("invalid policy was not rejected: %v", got)
				}
				if options.Policy != policy {
					t.Errorf("explicit policy %q silently became %q", policy, options.Policy)
				}
				if requests != 0 {
					t.Errorf("invalid policy queried remote %d times", requests)
				}
				entries, err := os.ReadDir(configDir)
				if err != nil || len(entries) != 0 {
					t.Errorf("invalid policy created resume state: entries=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestUploadPolicyDefaultsAndExplicitValues(t *testing.T) {
	for _, configured := range []string{baidupcs.SkipPolicy, baidupcs.OverWritePolicy, baidupcs.RsyncPolicy} {
		for _, provided := range []string{"", baidupcs.SkipPolicy, baidupcs.OverWritePolicy, baidupcs.RsyncPolicy} {
			t.Run(fmt.Sprintf("configured=%s/provided=%s", configured, provided), func(t *testing.T) {
				exportFixture(t)
				pcsconfig.Config.UPolicy = configured
				t.Setenv(pcsconfig.EnvConfigDir, t.TempDir())
				options := &UploadOptions{Policy: provided}
				var got error
				captureExportCall(t, func() { got = RunUpload([]string{t.TempDir()}, "/fixture", options) })
				if got == nil || !strings.Contains(got.Error(), "未检测到上传的文件") {
					t.Fatalf("valid policy rejected before source inspection: %v", got)
				}
				want := provided
				if want == "" {
					want = configured
				}
				if options.Policy != want {
					t.Errorf("selected policy=%q want %q", options.Policy, want)
				}
			})
		}
	}
}
