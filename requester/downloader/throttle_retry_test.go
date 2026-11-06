package downloader

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestSingleWorkerRetriesThrottledResponse(t *testing.T) {
	for _, status := range []int{429, 509} {
		for _, ranged := range []bool{false, true} {
			t.Run(fmt.Sprintf("status=%d/ranged=%v", status, ranged), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if requests.Add(1) == 1 {
						w.WriteHeader(status)
						return
					}
					if ranged {
						if r.Header.Get("Range") != "bytes=0-7" {
							t.Errorf("retry changed pending interval: %q", r.Header.Get("Range"))
						}
					} else {
						if r.Header.Get("Range") != "" {
							t.Error("full-stream retry requested a range")
						}
						w.Header().Set("ETag", `W/"fixture"`)
					}
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
				}))
				defer server.Close()
				f, err := os.Create(filepath.Join(t.TempDir(), "data"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024})
				info := &DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"}
				if !ranged {
					info.ETag = `W/"fixture"`
				}
				d.SetFirstInfo(info)
				ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
				defer cancel()
				if err := d.ExecuteWithContext(ctx); err != nil {
					t.Fatalf("single worker did not recover from throttling: requests=%d err=%v", requests.Load(), err)
				}
				got, err := os.ReadFile(f.Name())
				if err != nil || string(got) != "abcdefgh" || requests.Load() != 2 {
					t.Fatalf("retry result: data=%q requests=%d err=%v", got, requests.Load(), err)
				}
			})
		}
	}
}

func TestThrottledWorkerHonorsPauseAndRetryBudget(t *testing.T) {
	for _, tc := range []struct {
		name              string
		paused, exhausted bool
	}{
		{name: "allowed"},
		{name: "paused", paused: true},
		{name: "budget-exhausted", exhausted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitor := NewMonitor()
			monitor.lazyInit()
			monitor.resetController = NewResetController(1)
			if tc.exhausted {
				monitor.resetController.AddResetNum()
			}
			monitor.paused.Store(tc.paused)
			worker := NewWorker(0, "", nil)
			worker.SetAcceptRange("bytes")
			worker.SetRange(&transfer.Range{Begin: 0, End: 8})
			worker.status.SetStatusCode(StatusCodeTooManyConnections)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			worker.parentCtx = ctx
			resets := 0
			worker.resetFunc = func() { resets++ }
			monitor.Append(worker)
			monitor.ResetFailedAndNetErrorWorkers()
			worker.runWG.Wait()
			if tc.paused || tc.exhausted {
				if resets != 0 || worker.status.StatusCode() != StatusCodeTooManyConnections {
					t.Fatal("throttle retry bypassed pause or retry budget")
				}
			} else if resets != 1 || monitor.resetController.CanReset() {
				t.Fatalf("throttle retry did not consume the retry budget: resets=%d", resets)
			}
		})
	}
}
