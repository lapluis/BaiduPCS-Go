package pcsupload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestPreCanceledUploadRunDoesNotOpenOrResetSource(t *testing.T) {
	for _, mode := range []string{"missing", "opened", "oversized-cache"} {
		for _, expired := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/expired=%v", mode, expired), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "source")
				local := checksum.NewLocalFileChecksum(name, 4)
				if mode == "opened" {
					if err := os.WriteFile(name, []byte("source data"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := local.OpenPath(); err != nil {
						t.Fatal(err)
					}
					defer local.Close()
					if err := local.Sum(checksum.CHECKSUM_MD5); err != nil {
						t.Fatal(err)
					}
					if _, err := local.GetFile().Seek(2, io.SeekStart); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "oversized-cache" {
					local.Length = baidupcs.MaxUploadSize + 1
				}
				before, originalHandle := local.LocalFileMeta, local.GetFile()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var want error = context.Canceled
				if expired {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					want = context.DeadlineExceeded
				} else {
					cancel()
				}
				task := &UploadTaskUnit{Context: ctx, LocalFileChecksum: local, taskInfo: &taskframework.TaskInfo{}}
				result := task.Run()
				if result.Succeed || result.NeedRetry || !errors.Is(result.Err, want) {
					t.Fatalf("canceled upload did source work or lost cause: %+v", result)
				}
				if local.GetFile() != originalHandle || !reflect.DeepEqual(local.LocalFileMeta, before) {
					t.Fatal("canceled upload reset source metadata or replaced handle")
				}
				if originalHandle != nil {
					if position, err := originalHandle.Seek(0, io.SeekCurrent); err != nil || position != 2 {
						t.Fatalf("canceled upload closed or moved original handle: position=%d err=%v", position, err)
					}
				}
			})
		}
	}
}

func TestUploadRunRechecksCachedOversizeAgainstOpenedFile(t *testing.T) {
	name := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(name, []byte("small now"), 0600); err != nil {
		t.Fatal(err)
	}
	local := checksum.NewLocalFileChecksum(name, 4)
	local.Length = baidupcs.MaxUploadSize + 1
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	requests := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = uploadRoundTrip(func(*http.Request) (*http.Response, error) {
		requests++
		cancel() // Stop at preparation; this test never uploads source data.
		return nil, ctx.Err()
	})
	task := &UploadTaskUnit{Context: ctx, LocalFileChecksum: local, UploadingDatabase: &UploadingDatabase{}, PCS: baidupcs.NewPCSWithClient(1, client), SavePath: "/synthetic", taskInfo: &taskframework.TaskInfo{}}
	result := task.Run()
	if result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.Canceled) || requests != 1 || local.Length != int64(len("small now")) {
		t.Fatalf("stale size rejected current small file: requests=%d length=%d result=%+v", requests, local.Length, result)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != "small now" {
		t.Fatalf("preparation changed source: %q %v", got, err)
	}
}
