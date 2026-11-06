package pcsupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestRapidPreparationCancellationCoversEveryRequest(t *testing.T) {
	for _, phase := range []string{"list", "uk", "meta", "precreate"} {
		for _, bodyBlocked := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/headers", true: "/body"}[bodyBlocked], func(t *testing.T) {
				source := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(source, bytes.Repeat([]byte("abcdefgh"), 1024), 0600); err != nil {
					t.Fatal(err)
				}
				local := checksum.NewLocalFileChecksum(source, 4)
				if err := local.OpenPath(); err != nil {
					t.Fatal(err)
				}
				defer local.Close()
				started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var once sync.Once
				client := requester.NewHTTPClient()
				client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
					current := ""
					data := ""
					switch {
					case r.URL.Path == "/api/list":
						current, data = "list", `{"errno":0,"list":[]}`
					case r.URL.Path == "/api/user/getinfo":
						current, data = "uk", `{"errno":0,"records":[{"uk":7}]}`
					case r.URL.Query().Get("method") == "meta":
						current, data = "meta", `{"error_code":31066}`
					case r.URL.Path == "/api/precreate":
						current, data = "precreate", `{"errno":0,"return_type":1,"uploadid":"fixture"}`
					default:
						return nil, errors.New("unexpected preparation request")
					}
					if current == phase {
						if bodyBlocked {
							return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: &lifecycleResponseBody{ctx: r.Context(), release: release, started: started, closed: closed}}, nil
						}
						once.Do(func() { close(started) })
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-release:
							return nil, errors.New("fixture released")
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(data))}, nil
				})
				ctx, cancel := context.WithCancel(context.Background())
				task := &UploadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, SavePath: "/fixture", Policy: baidupcs.OverWritePolicy, taskInfo: &taskframework.TaskInfo{}, state: &uploader.InstanceState{}, panDir: "/", panFile: "fixture"}
				done := make(chan struct{})
				var next bool
				var result *taskframework.TaskUnitRunResult
				go func() { next, result = task.rapidUpload(); close(done) }()
				defer func() { cancel(); close(release); <-done }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("preparation phase did not start")
				}
				cancel()
				select {
				case <-done:
					if next || result == nil || result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.Canceled) {
						t.Fatalf("canceled phase continued: next=%v result=%+v", next, result)
					}
				case <-time.After(time.Second):
					t.Fatal("preparation request ignored cancellation")
				}
				if bodyBlocked {
					select {
					case <-closed:
					default:
						t.Error("canceled response body was not closed")
					}
				}
			})
		}
	}
}
