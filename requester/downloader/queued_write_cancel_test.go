package downloader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type queuedWriteTransport func(*http.Request) (*http.Response, error)

func (tr queuedWriteTransport) RoundTrip(r *http.Request) (*http.Response, error) { return tr(r) }

func TestQueuedDownloadWriteHonorsWorkerControls(t *testing.T) {
	for _, action := range []string{"none", "cancel", "parent", "pause", "reset"} {
		t.Run(action, func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			w := NewWorker(0, "", f)
			w.SetAcceptRange("bytes")
			w.SetTotalSize(8)
			w.SetRange(&transfer.Range{Begin: 0, End: 8})
			status := transfer.NewDownloadStatus()
			status.SetTotalSize(8)
			w.SetDownloadStatus(status)
			w.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh")))}
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			w.parentCtx = parent
			mu := &sync.Mutex{}
			w.SetWriteMutex(mu)
			mu.Lock()
			release := sync.OnceFunc(mu.Unlock)
			defer release()
			done := make(chan struct{})
			go func() { w.Execute(); close(done) }()
			deadline := time.After(time.Second)
			for w.GetStatus().StatusCode() != StatusCodeWaitToWrite {
				select {
				case <-done:
					t.Fatal("worker exited before write", w.Err())
				case <-deadline:
					release()
					<-done
					t.Fatal("worker did not queue")
				case <-time.After(time.Millisecond):
				}
			}
			switch action {
			case "cancel":
				if err := w.Cancel(); err != nil {
					t.Fatal(err)
				}
			case "parent":
				cancelParent()
			case "pause":
				w.Pause()
			case "reset":
				w.resetFunc()
			}
			release()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("queued worker did not exit")
			}
			data, err := os.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			if action == "none" {
				if string(data) != "abcdefgh" || status.Downloaded() != 8 || w.GetStatus().StatusCode() != StatusCodeSucceeded {
					t.Fatal("healthy write changed", string(data), w.Err())
				}
				return
			}
			want := StatusCodeCanceled
			if action == "pause" {
				want = StatusCodePaused
			}
			if action == "reset" {
				want = StatusCodeReseted
			}
			if len(data) != 0 || w.GetRange().LoadBegin() != 0 || status.Downloaded() != 0 || w.GetStatus().StatusCode() != want {
				t.Fatalf("canceled buffered write committed: action=%s data=%q begin=%d downloaded=%d state=%v", action, data, w.GetRange().LoadBegin(), status.Downloaded(), w.GetStatus().StatusCode())
			}
			if action == "cancel" || action == "parent" {
				if !errors.Is(w.Err(), context.Canceled) {
					t.Fatal("cancellation cause lost", w.Err())
				}
			} else if w.Err() != nil {
				t.Fatal("pause/reset became failure", w.Err())
			}
			if action == "pause" || action == "reset" {
				calls := 0
				client := requester.NewHTTPClient()
				client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Header.Get("Range") != "bytes=0-7" {
						t.Error("discarded buffer advanced resumed request", r.Header.Get("Range"))
					}
					return &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh"))), Request: r}, nil
				})
				w.SetClient(client)
				w.url = "http://download.fixture.invalid/file"
				if action == "pause" {
					w.Resume()
				} else {
					w.runAsync()
				}
				resumed := make(chan struct{})
				go func() { w.runWG.Wait(); close(resumed) }()
				select {
				case <-resumed:
				case <-time.After(time.Second):
					w.Cancel()
					t.Fatal("resumed write stalled")
				}
				data, err := os.ReadFile(f.Name())
				if err != nil || string(data) != "abcdefgh" || status.Downloaded() != 8 || calls != 1 || w.Err() != nil || w.GetStatus().StatusCode() != StatusCodeSucceeded {
					t.Fatal("resume did not reread and commit the pending range", string(data), calls, w.Err(), err)
				}
			}
		})
	}
}

type cancelingDownloadReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (r *cancelingDownloadReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.cancel()
	return n, err
}

func TestCanceledDownloadReadDoesNotWriteWithoutMutex(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := NewWorker(0, "", f)
	w.parentCtx = ctx
	w.SetWriteMutex(nil)
	w.SetAcceptRange("bytes")
	w.SetTotalSize(8)
	w.SetRange(&transfer.Range{Begin: 0, End: 8})
	w.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(&cancelingDownloadReader{r: bytes.NewReader([]byte("abcdefgh")), cancel: cancel})}
	w.Execute()
	info, err := f.Stat()
	if err != nil || info.Size() != 0 || w.GetRange().LoadBegin() != 0 || w.GetStatus().StatusCode() != StatusCodeCanceled || !errors.Is(w.Err(), context.Canceled) {
		t.Fatal("canceled read was committed without a write mutex", info, w.GetRange(), w.Err(), err)
	}
}

type controlledWriteAt struct {
	io.WriterAt
	after func()
}

func (w controlledWriteAt) WriteAt(p []byte, off int64) (int, error) {
	n, err := w.WriterAt.WriteAt(p, off)
	w.after()
	return n, err
}

func TestInFlightDownloadWriteKeepsBytesButHonorsControls(t *testing.T) {
	for _, action := range []string{"none", "cancel", "parent", "pause", "reset"} {
		t.Run(action, func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var w *Worker
			out := controlledWriteAt{WriterAt: f, after: func() {
				switch action {
				case "cancel":
					w.Cancel()
				case "parent":
					cancel()
				case "pause":
					w.Pause()
				case "reset":
					w.resetFunc()
				}
			}}
			w = NewWorker(0, "", out)
			w.parentCtx = ctx
			w.SetAcceptRange("bytes")
			w.SetTotalSize(8)
			w.SetRange(&transfer.Range{Begin: 0, End: 8})
			status := transfer.NewDownloadStatus()
			status.SetTotalSize(8)
			w.SetDownloadStatus(status)
			w.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh")))}
			w.Execute()
			data, err := os.ReadFile(f.Name())
			if err != nil || string(data) != "abcdefgh" || status.Downloaded() != 8 || w.GetRange().LoadBegin() != 8 {
				t.Fatal("committed bytes missing from checkpoint", string(data), status.Downloaded(), err)
			}
			want := StatusCodeSucceeded
			switch action {
			case "cancel", "parent":
				want = StatusCodeCanceled
			case "pause":
				want = StatusCodePaused
			case "reset":
				want = StatusCodeReseted
			}
			if w.GetStatus().StatusCode() != want {
				t.Fatal("write completion ignored control", action, w.GetStatus().StatusCode(), w.Err())
			}
			if action == "cancel" || action == "parent" {
				if !errors.Is(w.Err(), context.Canceled) {
					t.Fatal(w.Err())
				}
			} else if w.Err() != nil {
				t.Fatal(w.Err())
			}
		})
	}
}
