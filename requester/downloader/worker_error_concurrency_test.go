package downloader

import (
	"bytes"
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

func TestWorkerErrorObserversDuringRepeatedRequests(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, "https://fixture.invalid/file", f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	worker.parentCtx = ctx
	cause := errors.New("fixture network failure")
	calls := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
		mode := calls % 3
		calls++
		if mode == 0 {
			return nil, cause
		}
		if mode == 1 {
			return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil)), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusPartialContent, ContentLength: 8,
			Header: http.Header{"Content-Range": {"bytes 0-7/8"}}, Body: io.NopCloser(strings.NewReader("abcdefgh")), Request: r}, nil
	})
	worker.SetClient(client)
	const observers = 4
	ready, done := make(chan struct{}, observers), make(chan struct{})
	var wg sync.WaitGroup
	var reads atomic.Int64
	var invalidRate atomic.Bool
	for i := 0; i < observers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			for {
				select {
				case <-done:
					return
				default:
					if err := worker.Err(); err != nil {
						_ = err.Error()
						_ = errors.Is(err, cause)
					}
					if worker.GetSpeedsPerSecond() < 0 {
						invalidRate.Store(true)
					}
					reads.Add(1)
				}
			}
		}()
	}
	defer func() { close(done); wg.Wait() }()
	for i := 0; i < observers; i++ {
		<-ready
	}
	const requests = 96
	for i := 0; i < requests; i++ {
		worker.SetRange(&transfer.Range{Begin: 0, End: 8})
		worker.Execute()
		err := worker.Err()
		switch i % 3 {
		case 0:
			if !errors.Is(err, cause) || worker.GetStatus().StatusCode() != StatusCodeNetError {
				t.Fatalf("network error lost: status=%v err=%v", worker.GetStatus().StatusCode(), err)
			}
		case 1:
			if err == nil || !strings.Contains(err.Error(), "401") || worker.GetStatus().StatusCode() != StatusCodeInternalError {
				t.Fatalf("HTTP error lost: status=%v err=%v", worker.GetStatus().StatusCode(), err)
			}
		case 2:
			if err != nil || worker.GetStatus().StatusCode() != StatusCodeSucceeded {
				t.Fatalf("successful retry retained an error: status=%v err=%v", worker.GetStatus().StatusCode(), err)
			}
		}
	}
	data, err := os.ReadFile(f.Name())
	if err != nil || string(data) != "abcdefgh" || calls != requests || reads.Load() == 0 || invalidRate.Load() {
		t.Fatalf("repeated requests or observers did not complete: calls=%d reads=%d invalidRate=%t data=%q err=%v", calls, reads.Load(), invalidRate.Load(), data, err)
	}
}
