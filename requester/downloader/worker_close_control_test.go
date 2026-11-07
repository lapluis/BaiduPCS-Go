package downloader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestWorkerResponseCloseCanInvokeControls(t *testing.T) {
	for _, action := range []string{"cancel", "pause", "reset"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerCloseControlFixture$", "-test.count=1")
			cmd.Env = append(os.Environ(), "PCS_DOWNLOAD_CLOSE_CONTROL="+action)
			cmd.WaitDelay = time.Second
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("response close blocked controls or repeated cleanup: %v\n%s", err, output)
			}
		})
	}
}

type controlCallbackBody struct {
	started, closed     chan struct{}
	readOnce, closeOnce sync.Once
	onClose             func()
	calls               atomic.Int32
}

func (b *controlCallbackBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *controlCallbackBody) Close() error {
	b.calls.Add(1)
	b.closeOnce.Do(func() { b.onClose(); close(b.closed) })
	return nil
}

func TestWorkerCloseControlFixture(t *testing.T) {
	action := os.Getenv("PCS_DOWNLOAD_CLOSE_CONTROL")
	if action == "" {
		return
	}
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, "https://fixture.invalid/file", f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	worker.parentCtx = ctx
	var unexpected atomic.Int32
	client := requester.NewHTTPClient()
	client.Client.Transport = queuedWriteTransport(func(*http.Request) (*http.Response, error) {
		unexpected.Add(1)
		return nil, errors.New("paused worker unexpectedly started a request")
	})
	worker.SetClient(client)
	body := &controlCallbackBody{started: make(chan struct{}), closed: make(chan struct{}), onClose: worker.Pause}
	worker.firstResp = &http.Response{StatusCode: http.StatusPartialContent, ContentLength: 8,
		Header: http.Header{"Content-Range": {"bytes 0-7/8"}}, Body: body}
	done := make(chan struct{})
	go func() { worker.Execute(); close(done) }()
	select {
	case <-body.started:
	case <-ctx.Done():
		t.Fatal("worker did not read the response")
	}
	switch action {
	case "cancel":
		if err := worker.Cancel(); err != nil {
			t.Fatal(err)
		}
	case "pause":
		worker.Pause()
	case "reset":
		worker.Reset()
	default:
		t.Fatal("invalid fixture action", action)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("control did not join response cleanup")
	}
	worker.runWG.Wait()
	wantStatus := StatusCodePaused
	if action == "cancel" {
		wantStatus = StatusCodeCanceled
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 || worker.GetRange().LoadBegin() != 0 || body.calls.Load() != 1 || unexpected.Load() != 0 || worker.GetStatus().StatusCode() != wantStatus {
		t.Fatalf("control cleanup changed data or state: status=%v want=%v close=%d unexpected=%d err=%v", worker.GetStatus().StatusCode(), wantStatus, body.calls.Load(), unexpected.Load(), err)
	}
}
