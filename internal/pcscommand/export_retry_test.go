package pcscommand

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestExportSelectedDirectoryListingRetry(t *testing.T) {
	for _, stdout := range []bool{false, true} {
		for _, recoverListing := range []bool{false, true} {
			t.Run(fmt.Sprintf("stdout=%t/recover=%t", stdout, recoverListing), func(t *testing.T) {
				exportDirectoryFixture(t, false)
				client := GetBaiduPCS().GetClient()
				base := client.Client.Transport
				metadata, listings := 0, 0
				client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					if req.URL.Query().Get("method") == "meta" {
						metadata++
						return base.RoundTrip(req)
					}
					if req.URL.Path != "/api/list" || req.URL.Query().Get("dir") != "/tree" {
						t.Fatalf("unexpected request: %s", req.URL)
					}
					listings++
					if recoverListing && listings == 2 {
						return base.RoundTrip(req)
					}
					if req.Body != nil {
						req.Body.Close()
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"errno":-1,"errmsg":"fixture listing failure"}`)), Request: req}, nil
				})
				writer := &exportOutputFixture{}
				opens := 0
				var got error
				log := captureExportCall(t, func() {
					got = runExport([]string{"/tree"}, &ExportOptions{StdOut: stdout, SavePath: "fixture-output", MaxRetry: 1}, writer, func(string) (io.WriteCloser, error) { opens++; return writer, nil })
				})
				if metadata != 1 || listings != 2 {
					t.Errorf("metadata=%d listings=%d, want 1/2", metadata, listings)
				}
				wantOpens := 1
				if stdout {
					wantOpens = 0
				}
				if opens != wantOpens || writer.closes != wantOpens {
					t.Errorf("opens=%d closes=%d want %d", opens, writer.closes, wantOpens)
				}
				if recoverListing {
					if got != nil {
						t.Fatal(got)
					}
					if writer.data.String() != "BaiduPCS-Go mkdir \"/tree\"\n" || writer.writes != 1 {
						t.Errorf("successful retry lost output: %q writes=%d", writer.data.String(), writer.writes)
					}
					if stdout && !strings.Contains(log, "导出完毕") {
						t.Error("missing completion")
					}
				} else {
					var remote pcserror.Error
					if !errors.As(got, &remote) || remote.GetRemoteErrCode() != -1 {
						t.Errorf("exhausted listing error lost: %v", got)
					}
					if writer.writes != 0 || strings.Contains(log, "导出完毕") || strings.Contains(log, "导出成功") {
						t.Errorf("failed listing reported success: writes=%d log=%q", writer.writes, log)
					}
				}
			})
		}
	}
}
