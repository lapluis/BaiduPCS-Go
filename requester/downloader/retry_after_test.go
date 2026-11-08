package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func retryAfterWorker(t *testing.T, code int) *Worker {
	t.Helper()
	w := NewWorker(0, "fixture://retry-after", nil)
	w.SetAcceptRange("bytes")
	w.SetRange(&transfer.Range{Begin: 0, End: 8 << 20})
	w.SetTotalSize(8 << 20)
	w.SetDownloadStatus(transfer.NewDownloadStatus())
	w.firstResp = &http.Response{StatusCode: code, Status: fmt.Sprint(code), Header: http.Header{"Retry-After": []string{"60"}}, Body: io.NopCloser(strings.NewReader(""))}
	w.Execute()
	return w
}

func TestRetryAfterDeadlineFormsAndBounds(t *testing.T) {
	now := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	future := now.Add(time.Minute)
	for _, tc := range []struct {
		value string
		want  time.Time
	}{
		{"", time.Time{}}, {"0", time.Time{}}, {"1", now.Add(time.Second)},
		{" 60 \t", future}, {"0001", now.Add(time.Second)},
		{future.Format(http.TimeFormat), future}, {future.Format(time.RFC850), future}, {future.Format(time.ANSIC), future},
		{now.Format(http.TimeFormat), time.Time{}}, {now.Add(-time.Second).Format(http.TimeFormat), time.Time{}},
		{"-1", time.Time{}}, {"+1", time.Time{}}, {"0.5", time.Time{}}, {"invalid", time.Time{}},
		{"999999999999999999999z", time.Time{}},
		{"9223372036", now.Add(9223372036 * time.Second)},
		{"9223372037", now.Add(time.Duration(1<<63 - 1))},
		{"18446744073709551616", now.Add(time.Duration(1<<63 - 1))},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if got := retryAfterDeadline(tc.value, now); !got.Equal(tc.want) {
				t.Fatalf("deadline=%s, want %s", got, tc.want)
			}
		})
	}
}

func TestRetryAfterResumeWaitCanBePausedAndCanceled(t *testing.T) {
	w := retryAfterWorker(t, 429)
	var calls atomic.Int32
	client := requester.NewHTTPClient()
	client.Client.Transport = queuedWriteTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("unexpected request during Retry-After")
	})
	w.SetClient(client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.parentCtx = ctx
	join := func() {
		t.Helper()
		done := make(chan struct{})
		go func() { w.runWG.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cancel()
			<-done
			t.Fatal("wait did not stop promptly")
		}
	}
	for i := 0; i < 2; i++ {
		w.Pause()
		w.Resume()
		deadline := time.Now().Add(time.Second)
		for w.GetStatus().StatusCode() != StatusCodeRateLimited && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if w.GetStatus().StatusCode() != StatusCodeRateLimited {
			cancel()
			join()
			t.Fatalf("resume did not enter Retry-After wait: %v", w.GetStatus().StatusCode())
		}
		if i == 0 {
			w.Pause()
		} else {
			cancel()
		}
		join()
	}
	if calls.Load() != 0 {
		t.Fatalf("resume sent %d requests before Retry-After", calls.Load())
	}
}

func TestDownloadRetryAfterExpiryAllowsExactDownload(t *testing.T) {
	var calls atomic.Int32
	first := make(chan time.Time, 1)
	second := make(chan time.Time, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			first <- time.Now()
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		select {
		case second <- time.Now():
		default:
		}
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), strings.NewReader("abcdefgh"))
	}))
	defer server.Close()
	file, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	d := NewDownloader(server.URL, file, &Config{MaxParallel: 1, CacheSize: 1024})
	d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	when := <-second
	initial := <-first
	if when.Sub(initial) < time.Second || calls.Load() != 2 {
		t.Fatalf("retry timing or count wrong: delay=%s calls=%d", when.Sub(initial), calls.Load())
	}
	content, err := os.ReadFile(file.Name())
	if err != nil || string(content) != "abcdefgh" || d.monitor.Status().Downloaded() != 8 {
		t.Fatalf("download after wait corrupted: %q, %v", content, err)
	}
}

func TestRetryAfterBlocksRestartPathsWithoutSpendingBudget(t *testing.T) {
	for _, code := range []int{429, 503, 509} {
		for _, path := range []string{"failed", "stalled", "explicit"} {
			t.Run(fmt.Sprintf("%d/%s", code, path), func(t *testing.T) {
				w := retryAfterWorker(t, code)
				before := w.GetStatus().StatusCode()
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Bound a regression without allowing another request.
				w.parentCtx = ctx
				resets := 0
				w.resetFunc = func() { resets++ }
				m := NewMonitor()
				m.lazyInit()
				m.resetController = NewResetController(1)
				m.Append(w)
				switch path {
				case "failed":
					m.ResetFailedAndNetErrorWorkers()
				case "stalled":
					m.ResetWorker(w)
				default:
					w.Reset()
				}
				w.runWG.Wait()
				if resets != 0 || !m.resetController.CanReset() || w.GetStatus().StatusCode() != before {
					t.Fatalf("restart bypassed Retry-After: resets=%d budget=%t status=%v want=%v", resets, m.resetController.CanReset(), w.GetStatus().StatusCode(), before)
				}
			})
		}
	}
}

func TestRetryAfterBlocksDynamicSplit(t *testing.T) {
	w := retryAfterWorker(t, 503)
	m := NewMonitor()
	m.lazyInit()
	m.resetController = NewResetController(1)
	idle := NewWorker(1, "fixture://retry-after", nil)
	idle.SetAcceptRange("bytes")
	idle.SetRange(&transfer.Range{Begin: 8 << 20, End: 8 << 20})
	idle.status.SetStatusCode(StatusCodeSucceeded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idle.parentCtx = ctx
	m.Append(w)
	m.Append(idle)
	m.DynamicSplitWorker(w)
	idle.runWG.Wait()
	if w.GetRange().LoadEnd() != 8<<20 || idle.GetRange().LoadBegin() != 8<<20 || !m.resetController.CanReset() {
		t.Fatal("dynamic split bypassed Retry-After or consumed connection budget")
	}
}

func TestDownloadRetryAfterWaitIsCancelable(t *testing.T) {
	for _, code := range []int{429, 503, 509} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(code)
			}))
			defer server.Close()
			file, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			d := NewDownloader(server.URL, file, &Config{MaxParallel: 1, CacheSize: 1024})
			d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"})
			ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
			defer cancel()
			if err := d.ExecuteWithContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wait did not preserve cancellation: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("sent %d requests before Retry-After elapsed", calls.Load())
			}
			if info, err := file.Stat(); err != nil || info.Size() != 0 {
				t.Fatalf("error document was written: %v", err)
			}
		})
	}
}
