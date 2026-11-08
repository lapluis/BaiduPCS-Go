package pcscommand

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCopyMoveRejectsFailedDestinationInspection(t *testing.T) {
	for _, op := range []string{"copy", "move"} {
		for _, stage := range []string{"metadata", "target-pattern"} {
			t.Run(op+"/"+stage, func(t *testing.T) {
				exportFixture(t)
				requests, mutations := 0, 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						defer req.Body.Close()
					}
					method := req.URL.Query().Get("method")
					payload := `{"error_code":0}`
					if method == "meta" {
						payload = `{"error_code":31045,"error_msg":"fixture inspection denied"}`
					} else if req.URL.Path == "/api/list" {
						payload = `{"errno":2,"errmsg":"fixture pattern failure"}`
					} else {
						mutations++
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				target := "/target"
				if stage == "target-pattern" {
					target = "/target*"
				}
				output := captureExportCall(t, func() {
					if op == "copy" {
						RunCopy("/source", target)
					} else {
						RunMove("/source", target)
					}
				})
				if requests != 1 || mutations != 0 || strings.Contains(output, "成功") {
					t.Fatalf("failed destination inspection still mutated: requests=%d mutations=%d output=%s", requests, mutations, output)
				}
				if !strings.Contains(output, "fixture") && !strings.Contains(output, "31045") && !strings.Contains(output, "代码: 2") {
					t.Fatalf("inspection failure lost: %q", output)
				}
			})
		}
	}
}
