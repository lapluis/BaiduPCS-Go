package pcscommand

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func uploadSignalWaiters() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	stack := string(buf[:n])
	return strings.Count(stack, "pcscommand.RunUpload.func") + strings.Count(stack, "pcscommand.watchUploadSignals.func")
}

func TestUploadReturnsWithoutSignalWaiters(t *testing.T) {
	for _, kind := range []string{"empty-directory", "missing-path", "success", "task-failure"} {
		t.Run(kind, func(t *testing.T) {
			exportFixture(t) // Synthetic configuration only; no account is loaded.
			t.Setenv(pcsconfig.EnvConfigDir, t.TempDir())
			source := t.TempDir()
			if kind == "missing-path" {
				source = filepath.Join(source, "missing")
			}
			requests := 0
			if kind == "success" || kind == "task-failure" {
				source = filepath.Join(source, "fixture.txt")
				if err := os.WriteFile(source, []byte("abcd"), 0600); err != nil {
					t.Fatal(err)
				}
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						defer req.Body.Close()
					}
					if req.URL.Path != "/api/list" {
						t.Fatalf("unexpected upload request: %s", req.URL)
					}
					payload := `{"errno":0,"list":[{"fs_id":1,"path":"/fixture/fixture.txt","server_filename":"fixture.txt","size":4,"md5":"` + exportFixtureMD5 + `"}]}`
					if kind == "task-failure" {
						payload = `{"errno":2,"errmsg":"fixture task failure"}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
			}
			before := uploadSignalWaiters()
			for i := 0; i < 5; i++ {
				var err error
				captureExportCall(t, func() { err = RunUpload([]string{source}, "/fixture", &UploadOptions{Policy: baidupcs.SkipPolicy}) })
				if (err == nil) != (kind == "success") {
					t.Fatalf("unexpected upload result: %v", err)
				}
			}
			if after := uploadSignalWaiters(); after != before {
				t.Fatalf("upload retained signal goroutines after return: before=%d after=%d", before, after)
			}
			if kind == "success" || kind == "task-failure" {
				if requests == 0 {
					t.Fatal("upload did not reach fixture")
				}
				if data, err := os.ReadFile(source); err != nil || string(data) != "abcd" {
					t.Fatalf("source changed: %q %v", data, err)
				}
			}
		})
	}
}
