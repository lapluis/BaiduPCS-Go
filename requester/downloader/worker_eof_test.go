package downloader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type unexpectedDownloadEOF struct{ io.Reader }

func (r unexpectedDownloadEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func TestWorkerDoesNotSucceedOnIncompleteEOF(t *testing.T) {
	for _, single := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-range", true: "truncated-stream"}[single], func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			worker := NewWorker(0, "", f)
			if single {
				worker.firstResp = &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{}, Body: io.NopCloser(unexpectedDownloadEOF{bytes.NewReader([]byte("partial"))})}
			} else {
				worker.SetAcceptRange("bytes")
				worker.SetTotalSize(8)
				worker.SetRange(&transfer.Range{Begin: 0, End: 8})
				worker.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader(nil))}
			}
			worker.Execute()
			want := StatusCodeFailed
			if single {
				want = StatusCodeInternalError
			}
			if !errors.Is(worker.Err(), io.ErrUnexpectedEOF) || worker.GetStatus().StatusCode() != want {
				t.Fatalf("incomplete response succeeded: status=%v err=%v", worker.GetStatus().StatusCode(), worker.Err())
			}
		})
	}
}

func TestUnknownLengthTruncatedStreamReturnsError(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	original := []byte("old and longer content")
	if _, err := f.Write(original); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader("https://fixture.invalid/file", f, &Config{MaxParallel: 4, CacheSize: 1024})
	d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		return -1, &http.Response{StatusCode: 200, ContentLength: -1, Header: http.Header{}, Body: io.NopCloser(unexpectedDownloadEOF{bytes.NewReader([]byte("partial"))}), Request: req}, nil
	})
	var succeeded atomic.Bool
	d.OnSuccess(func() { succeeded.Store(true) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); !errors.Is(err, io.ErrUnexpectedEOF) || succeeded.Load() {
		t.Fatalf("truncated stream accepted: err=%v success=%v", err, succeeded.Load())
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || len(got) != len(original) || !bytes.Equal(got[len("partial"):], original[len("partial"):]) {
		t.Fatalf("failed stream truncated existing file: %q err=%v", got, err)
	}
}

func TestPrefixProbeEOFAtBufferBoundaryFetchesRemainder(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefgh"), 2048)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Range") != "bytes=8192-16383" {
			t.Errorf("wrong remainder: %q", r.Header.Get("Range"))
		}
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(data))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: f.Name() + ".state"})
	d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return 0, nil, err
		}
		return int64(len(data)), &http.Response{StatusCode: 206, ContentLength: 8192, Header: http.Header{"Content-Range": []string{"bytes 0-8191/16384"}}, Body: io.NopCloser(bytes.NewReader(data[:8192])), Request: req}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, data) || requests.Load() != 1 {
		t.Fatalf("prefix mistaken for complete file: requests=%d bytes=%d err=%v", requests.Load(), len(got), err)
	}
}
