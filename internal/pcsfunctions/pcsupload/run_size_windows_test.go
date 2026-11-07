package pcsupload

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"golang.org/x/sys/windows"
)

func TestUploadRunChecksFreshSparseSourceSizeBeforePreparation(t *testing.T) {
	for _, size := range []int64{baidupcs.MaxUploadSize, baidupcs.MaxUploadSize + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "sparse-source")
			file, err := os.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			// Set sparse before extending: only logical size changes, not a 128 GiB allocation.
			var returned uint32
			if err := windows.DeviceIoControl(windows.Handle(file.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(size); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			local := checksum.NewLocalFileChecksum(name, 4) // Cached length starts at zero.
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			requests := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = uploadRoundTrip(func(*http.Request) (*http.Response, error) {
				requests++
				// Guard the old implementation: stop at quota lookup, before any hashing.
				cancel()
				return nil, ctx.Err()
			})
			task := &UploadTaskUnit{Context: ctx, LocalFileChecksum: local, UploadingDatabase: &UploadingDatabase{}, PCS: baidupcs.NewPCSWithClient(1, client), SavePath: "/synthetic", taskInfo: &taskframework.TaskInfo{}}
			result := task.Run()
			rejected := size > baidupcs.MaxUploadSize
			if result.Succeed || result.NeedRetry || result.Err == nil || local.Length != size {
				t.Fatalf("current source size bypassed upload bound: requests=%d length=%d result=%+v", requests, local.Length, result)
			}
			if rejected {
				if result.ResultMessage != "文件大小超过128G, 无法上传" || requests != 0 || ctx.Err() != nil {
					t.Fatalf("oversized source entered preparation: requests=%d result=%+v", requests, result)
				}
			} else if !errors.Is(result.Err, context.Canceled) || requests != 1 {
				t.Fatalf("source at maximum size was rejected: requests=%d result=%+v", requests, result)
			}
			info, err := os.Stat(name)
			if err != nil || info.Size() != size {
				t.Fatalf("size rejection changed sparse source: %v %v", info, err)
			}
			if err := os.Rename(name, name+".released"); err != nil {
				t.Fatalf("size rejection left source open: %v", err)
			}
		})
	}
}
