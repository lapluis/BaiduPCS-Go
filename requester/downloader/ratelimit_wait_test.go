package downloader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestRateLimitedWorkerControls(t *testing.T) {
	for _, action := range []string{"pause", "cancel", "reset"} {
		t.Run(action, func(t *testing.T) {
			r := speeds.NewRateLimit(1)
			r.SetInterval(time.Hour)
			defer r.Stop()
			r.Add(1000000)
			f, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			w := NewWorker(0, "", f)
			w.SetAcceptRange("bytes")
			w.SetTotalSize(8)
			w.SetRange(&transfer.Range{Begin: 0, End: 8})
			w.firstResp = &http.Response{StatusCode: http.StatusPartialContent, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("01234567"))), ContentLength: 8}
			ds := transfer.NewDownloadStatus()
			ds.SetRateLimit(r)
			w.SetDownloadStatus(ds)
			done := make(chan struct{})
			go func() { w.Execute(); close(done) }()
			deadline := time.After(time.Second)
			for w.GetStatus().StatusCode() != StatusCodeRateLimited {
				select {
				case <-done:
					t.Fatalf("worker exited: %v", w.Err())
				case <-deadline:
					t.Fatal("no quota wait")
				case <-time.After(time.Millisecond):
				}
			}
			// The monitor must preserve this connection rather than reset a healthy wait.
			m := NewMonitor()
			m.lazyInit()
			m.ResetWorker(w)
			if w.GetStatus().StatusCode() != StatusCodeRateLimited {
				t.Fatal("monitor reset quota wait")
			}
			switch action {
			case "pause":
				w.Pause()
			case "cancel":
				w.Cancel()
			case "reset":
				w.resetFunc()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("quota wait ignored worker control")
			}
			want := StatusCodeReseted
			if action == "pause" {
				want = StatusCodePaused
			}
			if action == "cancel" {
				want = StatusCodeCanceled
			}
			if w.GetStatus().StatusCode() != want {
				t.Fatalf("state=%v err=%v", w.GetStatus().StatusCode(), w.Err())
			}
			if action == "cancel" && !errors.Is(w.Err(), context.Canceled) {
				t.Fatal(w.Err())
			}
			if w.GetRange().LoadBegin() != 0 || ds.Downloaded() != 0 {
				t.Fatal("unwritten bytes advanced checkpoint")
			}
			info, _ := f.Stat()
			if info.Size() != 0 {
				t.Fatal("canceled quota read written")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			if err := r.AddWithContext(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("worker control erased shared debt", err)
			}
		})
	}
}
