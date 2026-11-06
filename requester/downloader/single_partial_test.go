package downloader

import (
	"context"
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
)

func TestUnknownLengthPartialResponseCannotCompleteFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-2/*")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "abc")
	}))
	defer server.Close()
	filename := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(filename, []byte("existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filename, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, NewConfig())
	succeeded := false
	d.OnSuccess(func() { succeeded = true })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err == nil || succeeded {
		t.Errorf("partial response reported as a complete file: err=%v success=%v", err, succeeded)
	}
	got, err := os.ReadFile(filename)
	if err != nil || string(got) != "existing file" {
		t.Fatalf("partial response modified output: data=%q err=%v", got, err)
	}
}

func TestUnknownLengthPrefixProbeFetchesFullStream(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Range") != "" {
			t.Error("fallback requested a range instead of a full stream")
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "abcdefgh")
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, NewConfig())
	d.SetDURLCheckFuncWithContext(func(ctx context.Context, client *requester.HTTPClient, url string) (int64, *http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		return -1, &http.Response{StatusCode: 206, ContentLength: 3, Header: http.Header{"Content-Range": []string{"bytes 0-2/*"}}, Body: io.NopCloser(strings.NewReader("abc")), Request: req}, nil
	})
	if err := d.Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "abcdefgh" || requests.Load() != 1 {
		t.Fatalf("prefix accepted as complete stream: data=%q requests=%d err=%v", got, requests.Load(), err)
	}
}

func TestFullStreamWorkerRejectsPartialResponseBeforeWriting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Error("full-stream worker sent a range request")
		}
		w.Header().Set("Content-Range", "bytes 8-15/16")
		w.Header().Set("Content-Length", "8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "abcdefgh")
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, server.URL, f)
	worker.SetTotalSize(8)
	worker.Execute()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if worker.Err() == nil || worker.GetStatus().StatusCode() != StatusCodeInternalError || info.Size() != 0 || worker.GetRange().LoadBegin() != 0 {
		t.Fatalf("partial response consumed as full stream: err=%v status=%v size=%d range=%s", worker.Err(), worker.GetStatus().StatusCode(), info.Size(), worker.GetRange().ShowDetails())
	}
}
