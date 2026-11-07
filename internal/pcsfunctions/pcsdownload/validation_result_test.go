package pcsdownload

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestDownloadUnsupportedChecksumHasCleanTaskSuccess(t *testing.T) {
	installDownloadClient(t)
	for _, mode := range []string{"missing-blocks", "multiple-blocks", "matching", "wrong-digest"} {
		t.Run(mode, func(t *testing.T) {
			client := requester.NewHTTPClient()
			client.SetKeepAlive(false)
			client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("method") != "locatedownload" {
					t.Errorf("unexpected request: %s", req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error_code":0,"urls":[{"url":"https://fixture.invalid/file","encrypt":0}]}`)), Request: req}, nil
			})
			name := filepath.Join(t.TempDir(), "output")
			task := emptyTask(name, true, false)
			task.PCS = baidupcs.NewPCSWithClient(1, client)
			task.PCS.SetUID(1)
			task.FileInfo.Size = 8
			task.FileInfo.MD5 = fmt.Sprintf("%x", md5.Sum([]byte("01234567")))
			switch mode {
			case "multiple-blocks":
				task.FileInfo.BlockList = []string{"first", "second"}
			case "matching", "wrong-digest":
				task.FileInfo.BlockList = []string{"single block"}
			}
			if mode == "wrong-digest" {
				task.FileInfo.MD5 = strings.Repeat("0", 32)
			}
			task.DownloadStatistic = &DownloadStatistic{}
			result := task.Run()
			if mode == "wrong-digest" {
				if result.Succeed || !result.NeedRetry || !errors.Is(result.Err, ErrDownloadChecksumFailed) || task.DownloadStatistic.TotalSize() != 0 {
					t.Fatalf("incorrect digest lost failure: %+v", result)
				}
			} else if !result.Succeed || result.Err != nil || result.NeedRetry || result.NeedNextdindex || task.DownloadStatistic.TotalSize() != 8 {
				t.Fatalf("accepted download retained a failure: %+v bytes=%d", result, task.DownloadStatistic.TotalSize())
			}
			if got, err := os.ReadFile(name); err != nil || string(got) != "01234567" {
				t.Fatalf("download output changed: %q %v", got, err)
			}
			if mode == "missing-blocks" || mode == "multiple-blocks" {
				if err := CheckFileValid(name, task.FileInfo); !errors.Is(err, ErrDownloadNotSupportChecksum) {
					t.Fatalf("checksum API lost unsupported status: %v", err)
				}
			}
		})
	}
}

func TestDownloadLengthMismatchIncludesActualAndExpectedSizes(t *testing.T) {
	for _, length := range []int{0, 7, 9} {
		for _, skip := range []bool{false, true} {
			t.Run(fmt.Sprintf("size=%d/skip=%t", length, skip), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "output")
				content := strings.Repeat("x", length)
				if err := os.WriteFile(name, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name+DownloadSuffix, []byte("keep ranges"), 0600); err != nil {
					t.Fatal(err)
				}
				task := emptyTask(name, false, false)
				task.FileInfo.Size = 8
				task.NoCheck = skip
				result := &taskframework.TaskUnitRunResult{}
				if task.checkFileValid(result) || result.Succeed || !result.NeedRetry || !result.NeedNextdindex || !task.IsOverwrite || !errors.Is(result.Err, ErrDownloadLengthMismatch) {
					t.Fatalf("length mismatch lacks failure cause or re-download policy: %+v", result)
				}
				if !strings.Contains(result.Err.Error(), fmt.Sprintf("got %d, want 8", length)) {
					t.Fatalf("length failure omitted sizes: %v", result.Err)
				}
				for path, want := range map[string]string{name: content, name + DownloadSuffix: "keep ranges"} {
					if got, err := os.ReadFile(path); err != nil || string(got) != want {
						t.Fatalf("validation changed data: %q %v", got, err)
					}
				}
			})
		}
	}
}
