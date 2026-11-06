package pcsupload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestUploadPreparationCancelsLocalChecksumBeforePublishing(t *testing.T) {
	source := filepath.Join(t.TempDir(), "file")
	f, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(128 << 20); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	local := checksum.NewLocalFileChecksum(source, 4)
	if err := local.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	var requests atomic.Int32
	client := requester.NewHTTPClient()
	client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		if r.URL.Path != "/api/list" {
			t.Error("preparation sent another request after checksum cancellation")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(`{"errno":0,"list":[]}`))}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	task := &UploadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, SavePath: "/fixture", panDir: "/", panFile: "fixture", state: &uploader.InstanceState{}, taskInfo: &taskframework.TaskInfo{}}
	done := make(chan struct{})
	var next bool
	var result *taskframework.TaskUnitRunResult
	go func() { next, result = task.rapidUpload(); close(done) }()
	defer func() { cancel(); <-done }()
	for {
		pos, err := local.GetFile().Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		if pos >= int64(checksum.DefaultBufSize) {
			break
		}
		select {
		case <-done:
			t.Fatalf("fixture did not observe checksum progress: %+v", result)
		case <-ctx.Done():
			t.Fatal("checksum did not start reading")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	if next || result == nil || result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.Canceled) || local.MD5 != nil || local.SliceMD5 != nil || requests.Load() != 1 {
		t.Fatalf("task ignored checksum cancellation: next=%v result=%+v md5=%x requests=%d", next, result, local.MD5, requests.Load())
	}
}
