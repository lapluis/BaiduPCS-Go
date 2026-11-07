package downloader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

// Hold context construction to expose the scheduling window before the next
// request publishes its cancellation function.
type heldDownloadContext struct {
	context.Context
	entered, release chan struct{}
	once             sync.Once
}

func (ctx *heldDownloadContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.entered); <-ctx.release })
	return ctx.Context.Done()
}

func TestWorkerCancelDuringNextContextPublication(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, "https://fixture.invalid/file", f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	var calls atomic.Int32
	client := requester.NewHTTPClient()
	client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusPartialContent, ContentLength: 8,
			Header: http.Header{"Content-Range": {"bytes 0-7/8"}}, Body: io.NopCloser(strings.NewReader("abcdefgh")), Request: r}, nil
	})
	worker.SetClient(client)
	worker.Execute()
	if worker.GetStatus().StatusCode() != StatusCodeInternalError || calls.Load() != 1 {
		t.Fatal("first request did not finish with its own cancellation function")
	}
	held := &heldDownloadContext{Context: context.Background(), entered: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(held.release) })
	defer release()
	worker.parentCtx = held
	done := make(chan struct{})
	go func() { worker.Execute(); close(done) }()
	select {
	case <-held.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("next context construction did not start")
	}
	if err := worker.Cancel(); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("canceled context construction did not finish")
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 || calls.Load() != 1 || worker.GetStatus().StatusCode() != StatusCodeCanceled || !errors.Is(worker.Err(), context.Canceled) {
		t.Fatalf("accepted cancellation targeted the old request: calls=%d status=%v cause=%v stat=%v", calls.Load(), worker.GetStatus().StatusCode(), worker.Err(), err)
	}
}
