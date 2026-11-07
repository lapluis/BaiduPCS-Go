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
)

func TestSuppliedFirstInfoHonorsRangeCapability(t *testing.T) {
	for _, size := range []int64{8, -1} {
		for _, ranges := range []string{"", "none", "items", "bytes", "Bytes", "BYTES", "byteſ", "items, BYTES", "BYTES, items"} {
			t.Run(fmt.Sprintf("%d/%s", size, ranges), func(t *testing.T) {
				var calls atomic.Int32
				content := []byte("abcdefgh")
				wantRange := ""
				if size >= 0 && (ranges == "bytes" || ranges == "Bytes" || ranges == "BYTES" || ranges == "items, BYTES" || ranges == "BYTES, items") {
					wantRange = "bytes=0-7"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Header.Get("Range") != wantRange {
						t.Errorf("request ignored supplied capability: range=%q want=%q", r.Header.Get("Range"), wantRange)
					}
					if wantRange != "" {
						http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
						return
					}
					if size >= 0 {
						w.Header().Set("Content-Length", "8")
					}
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
					_, _ = w.Write(content)
				}))
				defer server.Close()
				f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				info := &DownloadFirstInfo{ContentLength: size, AcceptRanges: ranges}
				original := *info
				d := NewDownloader(server.URL, f, &Config{MaxParallel: 4})
				d.SetFirstInfo(info)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				err = d.ExecuteWithContext(ctx)
				data, readErr := os.ReadFile(f.Name())
				if err != nil || readErr != nil || !bytes.Equal(data, content) || calls.Load() != 1 || *info != original {
					t.Fatalf("supplied info did not download full entity: err=%v data=%q read=%v calls=%d info=%+v want=%+v", err, data, readErr, calls.Load(), info, original)
				}
				if d.monitor.Status().Downloaded() != 8 || d.monitor.Status().TotalSize() != 8 {
					t.Fatal("wrong final progress")
				}
			})
		}
	}
}

func TestSetFirstInfoKeepsCallerOwnership(t *testing.T) {
	info := &DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes", ETag: `W/"fixture"`}
	d := NewDownloader("https://fixture.invalid/file", nil, NewConfig())
	d.SetFirstInfo(info)
	info.ContentLength = 99
	info.AcceptRanges = "none"
	if d.firstInfo.ContentLength != 8 || d.firstInfo.AcceptRanges != "bytes" {
		t.Fatal("caller mutation changed configured transfer", d.firstInfo)
	}
	d.firstInfo.AcceptRanges = ""
	if info.AcceptRanges != "none" {
		t.Fatal("download normalization changed caller metadata", info)
	}
	d.SetFirstInfo(nil)
	if d.firstInfo != nil {
		t.Fatal("nil did not restore URL probing")
	}
}

func TestSuppliedUnknownSizeReplacesStaleRangedState(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Range") != "" {
			t.Error("unknown-size transfer resumed stale range", r.Header.Get("Range"))
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("new"))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("old data"); err != nil {
		t.Fatal(err)
	}
	statePath := f.Name() + ".state"
	state := openOwnedCheckpoint(t, statePath, InstanceStateStorageFormatJSON)
	if err := state.Put(checkpointOwnerInfo(4)); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 4, InstanceStatePath: statePath})
	d.SetFirstInfo(&DownloadFirstInfo{ContentLength: -1, AcceptRanges: "bytes"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = d.ExecuteWithContext(ctx)
	data, readErr := os.ReadFile(f.Name())
	if err != nil || readErr != nil || string(data) != "new" || calls.Load() != 1 {
		t.Fatal("single-stream replacement failed", err, string(data), readErr, calls.Load())
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("completed stream retained old ranges", err)
	}
}
