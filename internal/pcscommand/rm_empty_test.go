package pcscommand

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRemoveDoesNotSendEmptyDeletion(t *testing.T) {
	for _, pattern := range []bool{false, true} {
		name := "no-input"
		if pattern {
			name = "unmatched-pattern"
		}
		t.Run(name, func(t *testing.T) {
			exportFixture(t)
			requests, deletes := 0, 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"errno":0,"list":[]}`
				if req.URL.Query().Get("method") == "delete" {
					deletes++
					payload = `{"error_code":0}`
				} else if req.URL.Path != "/api/list" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			var paths []string
			if pattern {
				paths = []string{"/missing*"}
			}
			log := captureExportCall(t, func() { RunRemove(paths...) })
			wantRequests := 0
			if pattern {
				wantRequests = 1
			}
			if requests != wantRequests || deletes != 0 || strings.Contains(log, "成功") {
				t.Fatalf("empty selection sent deletion: requests=%d deletes=%d log=%q", requests, deletes, log)
			}
		})
	}
}
