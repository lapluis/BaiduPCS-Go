package pcsdownload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestTestDownloadDoesNotChangeExistingModificationTime(t *testing.T) {
	installDownloadClient(t)
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error_code":0,"urls":[{"url":"https://fixture.invalid/file","encrypt":0}]}`)), Request: req}, nil
	})
	name := filepath.Join(t.TempDir(), "existing")
	content := []byte("unrelated existing file")
	if err := os.WriteFile(name, content, 0600); err != nil {
		t.Fatal(err)
	}
	originalTime := time.Unix(1600000000, 0)
	if err := os.Chtimes(name, originalTime, originalTime); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	task := emptyTask(name, false, true)
	task.DownloadStatistic = &DownloadStatistic{}
	task.FileInfo.Size, task.FileInfo.Mtime = 8, 1700000000
	task.ModifyMTime = true
	task.PCS = baidupcs.NewPCSWithClient(1, client)
	task.PCS.SetUID(1)
	result := task.Run()
	if !result.Succeed || result.Err != nil {
		t.Fatalf("test download failed: %+v", result)
	}
	after, err := os.Stat(name)
	if err != nil || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("test download changed existing modification time: before=%v after=%v err=%v", before, after, err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != string(content) {
		t.Fatalf("test download changed content: %q %v", got, err)
	}
}

func TestDownloadCompletionReportsMetadataFailureBeforeStatistics(t *testing.T) {
	for _, mode := range []string{"success", "missing", "disabled", "test-mode", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "download")
			content := []byte("completed download")
			if mode != "missing" {
				if err := os.WriteFile(name, content, 0600); err != nil {
					t.Fatal(err)
				}
			}
			task := emptyTask(name, false, mode == "test-mode")
			task.DownloadStatistic = &DownloadStatistic{}
			task.DownloadStatistic.AddTotalSize(7)
			task.FileInfo.Size, task.FileInfo.Mtime = int64(len(content)), 1700000000
			task.ModifyMTime = mode != "disabled"
			if mode == "canceled" {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				task.Context = ctx
			}
			var before os.FileInfo
			if mode != "missing" {
				var err error
				before, err = os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
			}
			result := &taskframework.TaskUnitRunResult{}
			task.completeDownload(result)
			if mode == "missing" || mode == "canceled" {
				if result.Succeed || result.NeedRetry || task.DownloadStatistic.TotalSize() != 7 {
					t.Fatalf("failed finalization counted as success: %+v bytes=%d", result, task.DownloadStatistic.TotalSize())
				}
				if mode == "missing" {
					var pathErr *os.PathError
					if !errors.Is(result.Err, os.ErrNotExist) || !errors.As(result.Err, &pathErr) {
						t.Fatalf("metadata failure lost cause: %v", result.Err)
					}
				} else if !errors.Is(result.Err, context.Canceled) {
					t.Fatalf("completion ignored cancellation: %v", result.Err)
				}
			} else if !result.Succeed || result.Err != nil || task.DownloadStatistic.TotalSize() != 7+int64(len(content)) {
				t.Fatalf("healthy completion failed: %+v", result)
			}
			if mode != "missing" {
				after, err := os.Stat(name)
				if err != nil {
					t.Fatal(err)
				}
				want := before.ModTime()
				if mode == "success" {
					want = time.Unix(task.FileInfo.Mtime, 0)
				}
				if !after.ModTime().Equal(want) {
					t.Fatalf("unexpected modification time: got=%v want=%v", after.ModTime(), want)
				}
				if got, err := os.ReadFile(name); err != nil || string(got) != string(content) {
					t.Fatalf("completion changed payload: %q %v", got, err)
				}
			}
		})
	}
}
