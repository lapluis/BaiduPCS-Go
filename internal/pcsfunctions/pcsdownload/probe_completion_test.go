package pcsdownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type completionProbeBody struct {
	probeBody
	onClose func()
}

func (b *completionProbeBody) Close() error {
	b.probeBody.Close()
	if b.onClose != nil {
		b.onClose()
	}
	return nil
}

func TestProbeCompletionRetainsCancellationAndCallbackCause(t *testing.T) {
	for _, throughTask := range []bool{false, true} {
		for _, size := range []int64{0, 8} {
			for _, phase := range []string{"healthy", "callback-cancel", "close-cancel", "callback-panic-and-cancel"} {
				t.Run(fmt.Sprintf("task=%v/size=%d/%s", throughTask, size, phase), func(t *testing.T) {
					installDownloadClient(t)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					body := &completionProbeBody{}
					if phase == "close-cancel" {
						body.onClose = cancel
					}
					probes := 0
					panClient.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
						probes++
						return &http.Response{StatusCode: 200, ContentLength: size, Header: http.Header{"X-Bs-File-Size": []string{fmt.Sprint(size)}}, Body: body, Request: req}, nil
					})
					client := requester.NewHTTPClient()
					client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error_code":0,"urls":[{"url":"https://fixture.invalid/file","encrypt":0}]}`)), Request: req}, nil
					})
					root := t.TempDir()
					name := filepath.Join(root, "missing", "output")
					if size != 0 {
						name = filepath.Join(root, "existing")
						if err := os.WriteFile(name, []byte("keep local output"), 0600); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(name+DownloadSuffix, []byte("keep local ranges"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					task := emptyTask(name, true, false)
					task.ProbeOnly, task.Context = true, ctx
					task.FileInfo.Size = size
					task.PCS = baidupcs.NewPCSWithClient(1, client)
					task.PCS.SetUID(1)
					cause := errors.New("synthetic probe callback failure")
					callbacks := 0
					task.OnProbe = func(string, string) {
						callbacks++
						if phase == "callback-cancel" || phase == "callback-panic-and-cancel" {
							cancel()
						}
						if phase == "callback-panic-and-cancel" {
							panic(cause)
						}
					}
					var err error
					if throughTask {
						result := task.Run()
						err = result.Err
						if result.Succeed != (phase == "healthy") || result.NeedRetry {
							t.Fatalf("wrong probe result: %+v", result)
						}
					} else {
						err = task.download("https://fixture.invalid/file", task.panHTTPClient())
					}
					if phase == "healthy" {
						if err != nil {
							t.Fatalf("healthy probe failed: %v", err)
						}
					} else if !errors.Is(err, context.Canceled) {
						t.Fatalf("probe completion lost cancellation: %v", err)
					}
					if errors.Is(err, cause) != (phase == "callback-panic-and-cancel") {
						t.Fatalf("probe callback cause changed: %v", err)
					}
					if probes != 1 || callbacks != 1 || !body.closed || body.read {
						t.Fatalf("probe did extra work or leaked body: probes=%d callbacks=%d body=%+v", probes, callbacks, body)
					}
					if size == 0 {
						if _, err := os.Stat(filepath.Dir(name)); !os.IsNotExist(err) {
							t.Fatalf("probe created directory: %v", err)
						}
					} else {
						for path, want := range map[string]string{name: "keep local output", name + DownloadSuffix: "keep local ranges"} {
							if got, err := os.ReadFile(path); err != nil || string(got) != want {
								t.Fatalf("probe changed local data: %q %v", got, err)
							}
						}
					}
				})
			}
		}
	}
}
