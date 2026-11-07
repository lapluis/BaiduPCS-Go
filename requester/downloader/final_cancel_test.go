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

type finalCancelWriter struct {
	*os.File
	mode      string
	cancel    context.CancelFunc
	completed func() bool
	cause     error
	injected  bool
}

func (w *finalCancelWriter) Truncate(size int64) error {
	err := w.File.Truncate(size)
	if err == nil && (w.mode == "truncate-cancel" || w.mode == "truncate-error-cancel") {
		w.injected = true
		w.cancel()
		if w.mode == "truncate-error-cancel" {
			return w.cause
		}
	}
	return err
}

func (w *finalCancelWriter) Sync() error {
	err := w.File.Sync()
	if err == nil && w.completed() && (w.mode == "sync-cancel" || w.mode == "sync-error-cancel") {
		w.injected = true
		w.cancel()
		if w.mode == "sync-error-cancel" {
			return w.cause
		}
	}
	return err
}

func TestDownloadFinalCancellationPreventsSuccessAndCheckpointRemoval(t *testing.T) {
	for _, mode := range []string{"healthy", "progress-parent-cancel", "progress-downloader-cancel", "truncate-cancel", "sync-cancel", "truncate-error-cancel", "sync-error-cancel"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			started, release := make(chan struct{}), make(chan struct{})
			var startOnce, releaseOnce sync.Once
			releaseServer := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseServer()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Range", "bytes 0-7/8")
				w.Header().Set("Content-Length", "8")
				w.WriteHeader(http.StatusPartialContent)
				w.(http.Flusher).Flush()
				select {
				case <-started:
					_, _ = w.Write([]byte("complete"))
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			file, err := os.Create(filepath.Join(t.TempDir(), "output"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := file.WriteString("original-with-tail"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cause := errors.New("synthetic final write failure")
			writer := &finalCancelWriter{File: file, mode: mode, cancel: cancel, cause: cause}
			statePath := file.Name() + ".state"
			d := NewDownloader(server.URL, writer, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath})
			d.SetFileContentLength(8)
			writer.completed = func() bool {
				select {
				case <-d.monitor.CompletedChan():
					return true
				default:
					return false
				}
			}
			callbackExited := false
			var baseline []byte
			var baselineErr error
			d.OnDownloadStatusEvent(func(transfer.DownloadStatuser, func(RangeWorkerFunc)) {
				startOnce.Do(func() {
					baselineErr = d.monitor.saveCheckpoint()
					if baselineErr == nil {
						baseline, baselineErr = os.ReadFile(statePath)
					}
					close(started)
				})
				<-d.monitor.CompletedChan()
				if mode == "progress-parent-cancel" {
					cancel()
				}
				if mode == "progress-downloader-cancel" {
					d.Cancel()
				}
				callbackExited = true
			})
			var successes, finishes atomic.Int32
			d.OnSuccess(func() { successes.Add(1) })
			d.OnFinish(func() { finishes.Add(1) })
			runErr := d.ExecuteWithContext(ctx)
			releaseServer()
			if baselineErr != nil {
				t.Fatal(baselineErr)
			}
			if !callbackExited || finishes.Load() != 1 {
				t.Fatalf("completion did not join callback: exited=%t finishes=%d error=%v", callbackExited, finishes.Load(), runErr)
			}
			if mode == "healthy" {
				if runErr != nil || successes.Load() != 1 {
					t.Fatalf("healthy completion changed: success=%d error=%v", successes.Load(), runErr)
				}
				if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("healthy checkpoint not removed: %v", err)
				}
			} else {
				if !errors.Is(runErr, context.Canceled) || successes.Load() != 0 {
					t.Errorf("final cancellation reported success or lost cause: success=%d error=%v", successes.Load(), runErr)
				}
				if mode == "truncate-error-cancel" || mode == "sync-error-cancel" {
					if !errors.Is(runErr, cause) {
						t.Errorf("final write error lost: %v", runErr)
					}
				}
				if isFinalWriteCancellation(mode) && !writer.injected {
					t.Fatal("final operation injection did not run")
				}
				if _, err := os.Stat(statePath); err != nil {
					t.Errorf("canceled completion removed checkpoint: %v", err)
				} else {
					savedFile, err := os.OpenFile(statePath, os.O_RDWR, 0600)
					if err != nil {
						t.Fatal(err)
					}
					saved := NewInstanceState(savedFile, InstanceStateStorageFormatJSON)
					info, loadErr := saved.Load()
					closeErr := saved.Close()
					if loadErr != nil || closeErr != nil || info == nil || info.DownloadStatus == nil || info.DownloadStatus.TotalSize() != 8 {
						t.Fatalf("retained checkpoint cannot be reloaded: info=%+v load=%v close=%v", info, loadErr, closeErr)
					}
					for _, r := range info.Ranges {
						if mode != "sync-error-cancel" && r.Len() != 0 {
							t.Errorf("completed range lost from checkpoint: %+v", r)
						}
					}
					if mode == "sync-error-cancel" && !errors.Is(runErr, ErrCheckpointSave) {
						t.Errorf("failed final snapshot did not report save error: %v", runErr)
					}
					if mode == "sync-error-cancel" {
						if got, err := os.ReadFile(statePath); err != nil || string(got) != string(baseline) {
							t.Errorf("failed sync replaced the prior checkpoint: %q %v", got, err)
						}
					}
				}
			}
			want := "complete"
			if mode == "progress-parent-cancel" || mode == "progress-downloader-cancel" {
				want += "-with-tail"
			}
			if got, err := os.ReadFile(file.Name()); err != nil || string(got) != want {
				t.Errorf("incorrect final content: got=%q want=%q error=%v", got, want, err)
			}
		})
	}
}

func isFinalWriteCancellation(mode string) bool {
	return mode == "truncate-cancel" || mode == "sync-cancel" || mode == "truncate-error-cancel" || mode == "sync-error-cancel"
}
