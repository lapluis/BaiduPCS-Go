package downloader

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestDownloadWithPartialURLProbe(t *testing.T) {
	content := bytes.Repeat([]byte("partial-probe-content"), 100000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 2, CacheSize: 8192})
	d.SetDURLCheckFunc(func(client *requester.HTTPClient, url string) (int64, *http.Response, error) {
		resp, err := client.Req(http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-32767"})
		return int64(len(content)), resp, err
	})
	if err := d.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("partial probe corrupted download: got %d bytes", len(got))
	}
}

func TestWorkerValidatesResponseRangeBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		status       int
		valid        bool
	}{
		{"matching", "bytes 4-7/8", 206, true},
		{"wrong_begin", "bytes 0-3/8", 206, false},
		{"wrong_end", "bytes 4-6/8", 206, false},
		{"wrong_total", "bytes 4-7/9", 206, false},
		{"missing", "", 206, false},
		{"wrong_unit", "items 4-7/8", 206, false},
		{"overflow", "bytes 4-7/9223372036854775808", 206, false},
		{"unknown_total", "bytes 4-7/*", 206, false},
		{"ignored_range", "", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "bytes=4-7" {
					t.Errorf("unexpected request range: %s", r.Header.Get("Range"))
				}
				w.Header().Set("Content-Range", tc.header)
				w.Header().Set("Content-Length", "4")
				w.WriteHeader(tc.status)
				w.Write([]byte("4567"))
			}))
			defer server.Close()
			filename := filepath.Join(t.TempDir(), "data")
			original := []byte("xxxxxxxx")
			if err := os.WriteFile(filename, original, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(filename, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			worker := NewWorker(0, server.URL, f)
			worker.SetAcceptRange("bytes")
			worker.SetTotalSize(8)
			worker.SetRange(&transfer.Range{Begin: 4, End: 8})
			worker.Execute()
			got, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if tc.valid {
				if worker.Err() != nil || !bytes.Equal(got, []byte("xxxx4567")) {
					t.Fatalf("valid range failed: %q, %v", got, worker.Err())
				}
			} else {
				if worker.Err() == nil {
					t.Error("invalid response was accepted")
				}
				if !bytes.Equal(got, original) || worker.GetRange().LoadBegin() != 4 {
					t.Error("invalid response changed output or progress")
				}
			}
		})
	}
}

func TestWorkerInitialFullResponseCoversOnlyFirstBlock(t *testing.T) {
	for _, begin := range []int64{0, 4} {
		f, err := os.CreateTemp(t.TempDir(), "data")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		worker := NewWorker(0, "", f)
		worker.SetAcceptRange("bytes")
		worker.SetTotalSize(8)
		worker.SetRange(&transfer.Range{Begin: begin, End: begin + 4})
		worker.firstResp = &http.Response{StatusCode: http.StatusOK, ContentLength: 8, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader([]byte("01234567")))}
		worker.Execute()
		got, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		if begin == 0 {
			if worker.Err() != nil || string(got) != "0123" {
				t.Fatalf("initial full response: %q, %v", got, worker.Err())
			}
		} else if worker.Err() == nil || len(got) != 0 {
			t.Fatal("initial full response was accepted for a nonzero offset")
		}
	}
}

func TestWorkerResponseMatchesOriginalRequestAfterRangeSplit(t *testing.T) {
	pending := &transfer.Range{Begin: 4, End: 8}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=4-7" {
			t.Errorf("unexpected range: %s", r.Header.Get("Range"))
		}
		pending.StoreEnd(6)
		w.Header().Set("Content-Range", "bytes 4-7/8")
		w.Header().Set("Content-Length", "4")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("4567"))
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, server.URL, f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(pending)
	worker.Execute()
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if worker.Err() != nil || len(got) != 6 || string(got[4:]) != "45" || pending.LoadBegin() != 6 {
		t.Fatalf("split range: %q, %s, %v", got, pending.ShowDetails(), worker.Err())
	}
}
