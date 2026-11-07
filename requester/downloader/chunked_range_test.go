package downloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestWorkerChunkedRangeRetainsTruncatedPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=4-7" {
			t.Errorf("unexpected range %q", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Range", "bytes 4-7/8")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		io.WriteString(w, "45")
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("xxxxxxxx"); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(0, server.URL, f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 4, End: 8})
	worker.Execute()
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "xxxx45xx" || worker.GetRange().LoadBegin() != 6 || !errors.Is(worker.Err(), io.ErrUnexpectedEOF) || worker.GetStatus().StatusCode() != StatusCodeFailed {
		t.Fatalf("short chunked range lost pending bytes: got=%q begin=%d status=%v err=%v read=%v", got, worker.GetRange().LoadBegin(), worker.GetStatus().StatusCode(), worker.Err(), err)
	}
}

func TestDownloadChunkedRanges(t *testing.T) {
	for _, probe := range []bool{false, true} {
		t.Run(fmt.Sprintf("prefix-probe=%t", probe), func(t *testing.T) {
			const content = "abcdefgh"
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var begin, end int
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || end < begin || end >= len(content) {
					t.Errorf("invalid range %q", r.Header.Get("Range"))
					http.Error(w, "invalid range", http.StatusBadRequest)
					return
				}
				w.Header().Set("Accept-Ranges", "bytes")
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(content)))
				w.WriteHeader(http.StatusPartialContent)
				w.(http.Flusher).Flush()
				io.WriteString(w, content[begin:end+1])
			}))
			defer server.Close()
			f, err := os.CreateTemp(t.TempDir(), "data")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024})
			if probe {
				d.SetDURLCheckFunc(func(client *requester.HTTPClient, url string) (int64, *http.Response, error) {
					resp, err := client.Req(http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-3"})
					return int64(len(content)), resp, err
				})
			} else {
				d.SetFirstInfo(&DownloadFirstInfo{ContentLength: int64(len(content)), AcceptRanges: "bytes"})
			}
			success := false
			d.OnSuccess(func() { success = true })
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := d.ExecuteWithContext(ctx); err != nil || !success {
				t.Fatalf("chunked range stalled or failed: success=%t requests=%d err=%v", success, requests.Load(), err)
			}
			got, err := os.ReadFile(f.Name())
			wantRequests := int32(1)
			if probe {
				wantRequests = 2
			}
			if err != nil || string(got) != content || requests.Load() != wantRequests {
				t.Fatalf("chunked range corrupted content or retried: got=%q requests=%d err=%v", got, requests.Load(), err)
			}
		})
	}
}

func TestChunkedRangeRetryUsesCommittedOffset(t *testing.T) {
	var mu sync.Mutex
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		attempt := len(ranges)
		mu.Unlock()
		w.Header().Set("ETag", `"fixture"`)
		var body string
		switch attempt {
		case 1:
			w.Header().Set("Content-Range", "bytes 0-7/8")
			body = "abc" // End the response before the advertised range ends.
		case 2:
			w.Header().Set("Content-Range", "bytes 3-7/8")
			body = "defgh"
		default:
			http.Error(w, "unexpected retry", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		io.WriteString(w, body)
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024})
	d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes", ETag: `"fixture"`})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal("truncated chunked response did not recover", err)
	}
	mu.Lock()
	gotRanges := append([]string(nil), ranges...)
	mu.Unlock()
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "abcdefgh" || len(gotRanges) != 2 || gotRanges[0] != "bytes=0-7" || gotRanges[1] != "bytes=3-7" || d.monitor.Status().Downloaded() != 8 {
		t.Fatalf("retry lost committed offset or double-counted data: got=%q ranges=%v downloaded=%d err=%v", got, gotRanges, d.monitor.Status().Downloaded(), err)
	}
}
