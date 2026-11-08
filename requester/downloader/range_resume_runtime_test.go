package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestContextCancelPersistsRangesAndResumesExactBytes(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%t", chunked), func(t *testing.T) {
			testContextCancelPersistsRangesAndResumesExactBytes(t, chunked)
		})
	}
}

func testContextCancelPersistsRangesAndResumesExactBytes(t *testing.T, chunked bool) {
	t.Helper()
	data := bytes.Repeat([]byte("range-fixture-"), 150000)
	var resumedBytes atomic.Int64
	var requestsMu sync.Mutex
	var resumedRequests []transfer.Range
	started := make(chan struct{}, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A canceled request can reach the server after the second execution
		// starts. Identify its execution from the immutable request URL, rather
		// than counting late first-execution requests as resumed network bytes.
		resuming := r.URL.Query().Get("phase") == "resume"
		begin, end := int64(0), int64(len(data)-1)
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &begin, &end); err != nil {
			t.Errorf("invalid fixture range: %q", r.Header.Get("Range"))
			http.Error(w, "range", 400)
			return
		}
		if begin < 0 || end >= int64(len(data)) || end < begin {
			http.Error(w, "range", 416)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Accept-Ranges", "bytes")
		if !chunked {
			w.Header().Set("Content-Length", strconv.FormatInt(end-begin+1, 10))
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(data)))
		w.WriteHeader(206)
		if chunked {
			w.(http.Flusher).Flush()
		}
		if !resuming && !(begin == 0 && end == 32767) {
			last := min(end+1, begin+16384)
			_, _ = w.Write(data[begin:last])
			w.(http.Flusher).Flush()
			select {
			case started <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		if resuming {
			resumedBytes.Add(end - begin + 1)
			requestsMu.Lock()
			resumedRequests = append(resumedRequests, transfer.Range{Begin: begin, End: end + 1})
			requestsMu.Unlock()
		}
		_, _ = w.Write(data[begin : end+1])
	}))
	defer server.Close()
	filename := filepath.Join(t.TempDir(), "data")
	state := filename + ".state"
	makeDownload := func(ctx context.Context, f *os.File, phase string) *Downloader {
		d := NewDownloader(server.URL+"?phase="+phase, f, &Config{Mode: transfer.RangeGenMode_BlockSize, MaxParallel: 4, CacheSize: 8192, BlockSize: 128 * 1024, InstanceStatePath: state})
		d.SetDURLCheckFunc(func(c *requester.HTTPClient, url string) (int64, *http.Response, error) {
			resp, err := c.ReqWithContext(ctx, http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-32767"})
			if chunked && resp != nil && resp.ContentLength != -1 {
				t.Errorf("chunked fixture exposed a known length: %d", resp.ContentLength)
			}
			return int64(len(data)), resp, err
		})
		return d
	}
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := makeDownload(ctx, f, "initial")
	done := make(chan error, 1)
	go func() { done <- d.ExecuteWithContext(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		cancel()
		<-done
		f.Close()
		t.Fatal("ranges did not start")
	}
	deadline := time.Now().Add(time.Second)
	for {
		info, _ := f.Stat()
		if info != nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not join range requests")
	}
	f.Close()
	if _, err := os.Stat(state); err != nil {
		t.Fatal("canceled task lost resume state", err)
	}
	savedCheckpoint, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	f, err = os.OpenFile(filename, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	err = makeDownload(ctx2, f, "resume").ExecuteWithContext(ctx2)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("resumed payload differs", err)
	}
	if resumedBytes.Load() >= int64(len(data)+32768) {
		requestsMu.Lock()
		defer requestsMu.Unlock()
		t.Fatalf("completed bytes were downloaded again: %d; checkpoint=%s requests=%v", resumedBytes.Load(), savedCheckpoint, resumedRequests)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("completed task retained state")
	}
}

func TestRangeIgnoredFallsBackToOneStream(t *testing.T) {
	data := bytes.Repeat([]byte("fixture"), 1000)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 4, CacheSize: 1024, InstanceStatePath: f.Name() + ".state"})
	d.SetDURLCheckFunc(func(c *requester.HTTPClient, url string) (int64, *http.Response, error) {
		resp, err := c.ReqWithContext(ctx, http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-15"})
		return int64(len(data)), resp, err
	})
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ignored Range still opened %d requests", calls.Load())
	}
	got, _ := os.ReadFile(f.Name())
	if !bytes.Equal(got, data) {
		t.Fatal("fallback stream corrupted payload")
	}
}

func TestChangedEntityRejectedBeforeRangeWrite(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1024*1024)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		begin, end := int64(0), int64(len(data)-1)
		fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &begin, &end)
		etag := `"v1"`
		if calls.Add(1) > 1 {
			etag = `"v2"`
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(data)))
		w.Header().Set("Content-Length", strconv.FormatInt(end-begin+1, 10))
		w.WriteHeader(206)
		_, _ = w.Write(data[begin : end+1])
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	d := NewDownloader(server.URL, f, &Config{Mode: transfer.RangeGenMode_BlockSize, MaxParallel: 4, CacheSize: 1024, BlockSize: 128 * 1024, InstanceStatePath: f.Name() + ".state"})
	d.SetDURLCheckFunc(func(c *requester.HTTPClient, url string) (int64, *http.Response, error) {
		resp, err := c.ReqWithContext(ctx, http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-15"})
		return int64(len(data)), resp, err
	})
	if err := d.ExecuteWithContext(ctx); !errors.Is(err, ErrRemoteChanged) {
		t.Fatalf("changed entity reported %v", err)
	}
}

func TestRangeHTTPFailuresKeepEntityAndProgress(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   StatusCode
	}{
		{400, StatusCodeInternalError}, {401, StatusCodeInternalError},
		{405, StatusCodeInternalError}, {410, StatusCodeInternalError},
		{403, StatusCodeInternalError}, {404, StatusCodeInternalError},
		{429, StatusCodeTooManyConnections}, {509, StatusCodeTooManyConnections},
		{500, StatusCodeNetError},
	} {
		t.Run(strconv.Itoa(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"error-document"`)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte("error"))
			}))
			defer server.Close()
			name := filepath.Join(t.TempDir(), "partial")
			if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(name, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			worker := NewWorker(0, server.URL, f)
			worker.SetAcceptRange("bytes")
			worker.SetTotalSize(8)
			worker.SetRange(&transfer.Range{Begin: 4, End: 8})
			worker.etag = `"file-entity"`
			worker.Execute()
			if worker.Err() == nil || errors.Is(worker.Err(), ErrRemoteChanged) || worker.GetStatus().StatusCode() != tc.want {
				t.Fatalf("HTTP failure misclassified: err=%v status=%v want=%v", worker.Err(), worker.GetStatus().StatusCode(), tc.want)
			}
			got, err := os.ReadFile(name)
			if err != nil || string(got) != "original" || worker.GetRange().LoadBegin() != 4 {
				t.Fatal("HTTP failure modified file or range")
			}
		})
	}
}

func TestWeakEntityTagUsesOneCompleteResponse(t *testing.T) {
	for _, modified := range []string{"", "Wed, 21 Oct 2015 07:28:00 GMT"} {
		t.Run(fmt.Sprint(modified != ""), func(t *testing.T) {
			data := bytes.Repeat([]byte("weak-entity-fixture"), 1000)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := calls.Add(1)
				w.Header().Set("ETag", `W/"semantic-version"`)
				w.Header().Set("Last-Modified", modified)
				w.Header().Set("Accept-Ranges", "bytes")
				if call == 1 {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-15/%d", len(data)))
					w.Header().Set("Content-Length", "16")
					w.WriteHeader(206)
					_, _ = w.Write(data[:16])
					return
				}
				if r.Header.Get("If-Range") != "" || r.Header.Get("Range") != "" {
					t.Errorf("weak entity still split into ranges: %v", r.Header)
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				_, _ = w.Write(data)
			}))
			defer server.Close()
			f, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 4, CacheSize: 1024, InstanceStatePath: f.Name() + ".state"})
			d.SetDURLCheckFunc(func(c *requester.HTTPClient, url string) (int64, *http.Response, error) {
				resp, err := c.ReqWithContext(ctx, http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-15"})
				return int64(len(data)), resp, err
			})
			if err := d.ExecuteWithContext(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(f.Name())
			if err != nil || !bytes.Equal(got, data) || calls.Load() != 2 {
				t.Fatalf("wrong single stream: calls=%d bytes=%d err=%v", calls.Load(), len(got), err)
			}
		})
	}
}
