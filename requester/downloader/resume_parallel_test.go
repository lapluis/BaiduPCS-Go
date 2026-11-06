package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestResumeParallelRespectsCurrentLimit(t *testing.T) {
	ranges := make(transfer.RangeList, 4)
	for _, limit := range []int{1, 2, 4} {
		if got := (&Downloader{}).SelectParallel(false, limit, 32, ranges); got != limit {
			t.Errorf("resume workers=%d, limit=%d", got, limit)
		}
	}
}

func TestReducedResumeParallelPreservesQueuedRangesAcrossCancel(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, mode := range []transfer.RangeGenMode{transfer.RangeGenMode_Default, transfer.RangeGenMode_BlockSize} {
			t.Run(fmt.Sprintf("%d/%d", format, mode), func(t *testing.T) {
				data := []byte("0123456789abcdefghijklmnopqrstuv")
				initial := append([]byte(nil), data...)
				ranges := transfer.RangeList{{Begin: 1, End: 3}, {Begin: 5, End: 7}, {Begin: 9, End: 11}, {Begin: 13, End: 15}}
				for _, r := range ranges {
					clear(initial[r.Begin:r.End])
				}
				generated, remaining := int64(32), int64(8)
				if mode == transfer.RangeGenMode_BlockSize {
					generated = 24
					remaining += 8
					clear(initial[24:])
				}
				var stalled atomic.Bool
				stalled.Store(true)
				var active, peak, sent atomic.Int64
				started := make(chan struct{}, 4)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("ETag", `"fixture"`)
					w.Header().Set("Accept-Ranges", "bytes")
					if r.Header.Get("Range") == "" {
						w.Header().Set("Content-Length", "32")
						_, _ = w.Write(data)
						return
					}
					n := active.Add(1)
					defer active.Add(-1)
					for old := peak.Load(); n > old; old = peak.Load() {
						if peak.CompareAndSwap(old, n) {
							break
						}
					}
					var begin, end int64
					if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || end >= 32 || begin > end {
						http.Error(w, "range", 416)
						return
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/32", begin, end))
					w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
					w.WriteHeader(206)
					if stalled.Load() {
						w.(http.Flusher).Flush()
						select {
						case started <- struct{}{}:
						default:
						}
						<-r.Context().Done()
						return
					}
					_, _ = w.Write(data[begin : end+1])
					sent.Add(end - begin + 1)
				}))
				defer server.Close()
				dir := t.TempDir()
				name, state := filepath.Join(dir, "data"), filepath.Join(dir, "state")
				if err := os.WriteFile(name, initial, 0600); err != nil {
					t.Fatal(err)
				}
				exported := &transfer.DownloadInstanceInfoExport{TotalSize: 32, GenBegin: generated, BlockSize: 4, RangeGenMode: mode, Ranges: ranges}
				var encoded []byte
				var err error
				if format == InstanceStateStorageFormatProto3 {
					encoded, err = proto.Marshal(exported)
				} else {
					encoded, err = json.Marshal(exported)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(state, encoded, 0600); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(name, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				makeDownload := func() *Downloader {
					return NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024, Mode: mode, InstanceStatePath: state, InstanceStateStorageFormat: format})
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() { done <- makeDownload().ExecuteWithContext(ctx) }()
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					cancel()
					<-done
					t.Fatal("resume did not start")
				}
				// Leave time for an incorrectly restored four-worker pool to connect.
				time.Sleep(50 * time.Millisecond)
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("resume did not cancel")
				}
				deadline := time.Now().Add(time.Second)
				for active.Load() != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
				if active.Load() != 0 {
					t.Fatal("canceled requests remained live")
				}
				stalled.Store(false)
				ctx2, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				if err := makeDownload().ExecuteWithContext(ctx2); err != nil {
					t.Fatal(err)
				}
				got, err := os.ReadFile(name)
				if err != nil || !bytes.Equal(got, data) {
					t.Fatal("queued ranges lost on cancel/resume", err)
				}
				if peak.Load() > 1 {
					t.Errorf("resume exceeded current connection limit: %d", peak.Load())
				}
				if sent.Load() != remaining {
					t.Errorf("transferred %d bytes, expected only %d pending bytes", sent.Load(), remaining)
				}
			})
		}
	}
}
