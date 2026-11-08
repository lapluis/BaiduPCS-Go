package pcscommand

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestDownloadReturnsListingAndTaskErrors(t *testing.T) {
	for _, kind := range []string{"missing", "directory-list", "output", "partial", "two-errors", "empty-file", "empty-directory"} {
		t.Run(kind, func(t *testing.T) {
			exportFixture(t)
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := ""
				switch {
				case req.URL.Query().Get("method") == "meta":
					if err := req.ParseMultipartForm(4096); err != nil {
						t.Fatal(err)
					}
					if req.MultipartForm != nil {
						defer req.MultipartForm.RemoveAll()
					}
					var params baidupcs.PathsListJSON
					if err := json.Unmarshal([]byte(req.FormValue("param")), &params); err != nil {
						t.Fatal(err)
					}
					if len(params.List) != 1 || params.List[0] == nil {
						t.Fatal("unexpected metadata input")
					}
					p := params.List[0].Path
					if p == "/missing.txt" {
						payload = `{"error_code":31066,"error_msg":"fixture missing"}`
					} else {
						dir := 0
						if p == "/tree" {
							dir = 1
						} else if p != "/fixture.txt" {
							t.Fatalf("unexpected metadata path: %q", p)
						}
						payload = fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":1,"path":%q,"size":0,"isdir":%d}]}`, p, dir)
					}
				case req.URL.Path == "/api/list" && req.URL.Query().Get("dir") == "/tree":
					payload = `{"errno":0,"list":[]}`
					if kind == "directory-list" {
						payload = `{"errno":2,"errmsg":"fixture listing failure"}`
					}
				default:
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			paths := []string{"/fixture.txt"}
			if kind == "missing" {
				paths = []string{"/missing.txt"}
			}
			if kind == "directory-list" || kind == "empty-directory" {
				paths = []string{"/tree"}
			}
			if kind == "partial" || kind == "two-errors" {
				paths = append(paths, "/missing.txt")
			}
			target := t.TempDir()
			blocker := filepath.Join(target, "retained")
			if kind == "output" || kind == "two-errors" {
				if err := os.WriteFile(blocker, []byte("sole-copy"), 0600); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(blocker, "child")
			}
			var got error
			log := captureExportCall(t, func() { got = RunDownloadWithError(paths, &DownloadOptions{SaveTo: target, MaxRetry: 0}) })
			success := kind == "empty-file" || kind == "empty-directory"
			if (got == nil) != success {
				t.Fatalf("unexpected download result: %v", got)
			}
			if strings.Contains(log, "下载结束") != success {
				t.Errorf("incorrect completion: %q", log)
			}
			wantCalls := 1
			if len(paths) == 2 || kind == "directory-list" || kind == "empty-directory" {
				wantCalls = 2
			}
			if calls != wantCalls {
				t.Errorf("requests=%d want %d", calls, wantCalls)
			}
			if kind == "missing" || kind == "partial" || kind == "two-errors" || kind == "directory-list" {
				var remote pcserror.Error
				code := 31066
				if kind == "directory-list" {
					code = 2
				}
				if !errors.As(got, &remote) || remote.GetRemoteErrCode() != code {
					t.Errorf("listing cause lost: %v", got)
				}
			}
			if kind == "output" || kind == "two-errors" {
				var pathErr *os.PathError
				if !errors.As(got, &pathErr) || !strings.Contains(got.Error(), "下载失败文件数: 1") {
					t.Errorf("output failure lost: %v", got)
				}
				if data, err := os.ReadFile(blocker); err != nil || string(data) != "sole-copy" {
					t.Errorf("existing output changed: %q %v", data, err)
				}
			}
			if kind == "empty-file" || kind == "partial" {
				info, err := os.Stat(filepath.Join(target, "fixture.txt"))
				if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
					t.Errorf("valid output missing: %v", err)
				}
			}
			if kind == "empty-directory" {
				info, err := os.Stat(filepath.Join(target, "tree"))
				if err != nil || !info.IsDir() {
					t.Errorf("empty directory missing: %v", err)
				}
			}
		})
	}
}
