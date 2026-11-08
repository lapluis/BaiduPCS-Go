package pcscommand

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

func TestUploadStoppedQueueDoesNotReportSuccess(t *testing.T) {
	for _, phase := range []string{"before-dispatch", "after-success", "after-failure", "completed"} {
		t.Run(phase, func(t *testing.T) {
			exportFixture(t)
			t.Setenv(pcsconfig.EnvConfigDir, t.TempDir())
			var paths []string
			for _, name := range []string{"first.txt", "second.txt"} {
				p := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(p, []byte("abcd"), 0600); err != nil {
					t.Fatal(err)
				}
				paths = append(paths, p)
			}
			var executor *taskframework.TaskExecutor
			requests, cleanups := 0, 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Path != "/api/list" || req.URL.Query().Get("dir") != "/fixture" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				if phase != "completed" {
					executor.Stop()
				}
				payload := `{"errno":0,"list":[{"fs_id":1,"path":"/fixture/first.txt","server_filename":"first.txt","size":4,"md5":"` + exportFixtureMD5 + `"},{"fs_id":2,"path":"/fixture/second.txt","server_filename":"second.txt","size":4,"md5":"` + exportFixtureMD5 + `"}]}`
				if phase == "after-failure" {
					payload = `{"errno":2,"errmsg":"fixture failure"}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			var got error
			log := captureExportCall(t, func() {
				got = runUpload(paths, "/fixture", &UploadOptions{Load: 1, Policy: baidupcs.SkipPolicy}, func(e *taskframework.TaskExecutor) func() {
					executor = e
					if phase == "before-dispatch" {
						e.Stop()
					}
					return func() { cleanups++ }
				})
			})
			wantPending, wantRequests := 1, 1
			if phase == "before-dispatch" {
				wantPending, wantRequests = 2, 0
			}
			if phase == "completed" {
				wantPending = 0
			}
			if executor.Count() != wantPending || requests != wantRequests || cleanups != 1 {
				t.Fatalf("pending=%d requests=%d cleanups=%d; want %d/%d/1", executor.Count(), requests, cleanups, wantPending, wantRequests)
			}
			if phase == "completed" {
				if got != nil || !strings.Contains(log, "上传结束") {
					t.Fatalf("normal upload failed: %v log=%q", got, log)
				}
			} else {
				if !errors.Is(got, context.Canceled) {
					t.Errorf("stopped upload lost cancellation: %v", got)
				}
				if !strings.Contains(log, "上传已取消") || strings.Contains(log, "上传结束") {
					t.Errorf("stopped upload reported completion: %q", log)
				}
				if phase == "after-failure" && (got == nil || !strings.Contains(got.Error(), "上传失败文件数: 1")) {
					t.Errorf("cancellation discarded task failure: %v", got)
				}
			}
			for _, p := range paths {
				if data, err := os.ReadFile(p); err != nil || string(data) != "abcd" {
					t.Errorf("source changed: %q %v", data, err)
				}
			}
		})
	}
}
