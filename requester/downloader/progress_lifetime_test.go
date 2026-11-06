package downloader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestDownloadJoinsProgressCallbackBeforeFinalEvents(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "canceled"}[cancelTask], func(t *testing.T) {
			networkRelease, callbackRelease := make(chan struct{}), make(chan struct{})
			releaseNetwork := sync.OnceFunc(func() { close(networkRelease) })
			releaseCallback := sync.OnceFunc(func() { close(callbackRelease) })
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Range", "bytes 0-7/8")
				w.Header().Set("Content-Length", "8")
				w.WriteHeader(206)
				w.(http.Flusher).Flush()
				select {
				case <-networkRelease:
					_, _ = w.Write([]byte("complete"))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			f, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: f.Name() + ".state"})
			d.SetFileContentLength(8)
			started, exited := make(chan struct{}), make(chan struct{})
			var startOnce, exitOnce sync.Once
			d.OnDownloadStatusEvent(func(transfer.DownloadStatuser, func(RangeWorkerFunc)) {
				startOnce.Do(func() { close(started) })
				<-callbackRelease
				exitOnce.Do(func() { close(exited) })
			})
			var invalidOrder atomic.Bool
			assertStopped := func() {
				select {
				case <-exited:
				default:
					invalidOrder.Store(true)
				}
			}
			d.OnSuccess(assertStopped)
			d.OnFinish(assertStopped)
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- d.ExecuteWithContext(ctx) }()
			joined := false
			defer func() {
				cancel()
				releaseNetwork()
				releaseCallback()
				if !joined {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("download did not exit during cleanup")
					}
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("progress callback did not start")
			}
			if cancelTask {
				cancel()
			} else {
				releaseNetwork()
			}
			var runErr error
			returnedEarly := false
			select {
			case runErr = <-done:
				joined = true
				returnedEarly = true
			case <-time.After(200 * time.Millisecond):
			}
			releaseCallback()
			if !joined {
				select {
				case runErr = <-done:
					joined = true
				case <-time.After(3 * time.Second):
					t.Fatal("download did not join callback")
				}
			}
			select {
			case <-exited:
			case <-time.After(time.Second):
				t.Fatal("progress callback left running")
			}
			if returnedEarly || invalidOrder.Load() {
				t.Error("download returned or emitted final events while progress callback was still running")
			}
			if cancelTask {
				if !errors.Is(runErr, context.Canceled) {
					t.Fatal(runErr)
				}
			} else if runErr != nil {
				t.Fatal(runErr)
			}
		})
	}
}
