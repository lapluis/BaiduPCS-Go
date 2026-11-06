package downloader

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUnknownLengthDownloadFinalizesActualSize(t *testing.T) {
	for _, content := range []string{"", "new"} {
		t.Run(content, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush() // Force an unknown-length chunked response.
				_, _ = io.WriteString(w, content)
			}))
			defer server.Close()
			filename := filepath.Join(t.TempDir(), "data")
			if err := os.WriteFile(filename, []byte("old and longer content"), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(filename, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, NewConfig())
			d.OnSuccess(func() {
				info, err := f.Stat()
				if err != nil || info.Size() != int64(len(content)) {
					t.Errorf("success announced before setting final size: info=%v err=%v", info, err)
				}
			})
			if err := d.Execute(); err != nil {
				t.Fatal(err)
			}
			if d.firstInfo.ContentLength != -1 {
				t.Fatal("fixture did not exercise an unknown length")
			}
			got, err := os.ReadFile(filename)
			if err != nil || string(got) != content {
				t.Fatalf("unknown-length download retained old tail: %q err=%v", got, err)
			}
		})
	}
}

func TestUnknownLengthDownloadReturnsFinalSizeFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "new")
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	failure := errors.New("injected unknown-length truncate failure")
	d := NewDownloader(server.URL, truncateFailureWriter{File: f, failure: failure}, NewConfig())
	succeeded := false
	d.OnSuccess(func() { succeeded = true })
	if err := d.Execute(); !errors.Is(err, failure) || succeeded {
		t.Fatalf("failed finalization accepted: err=%v succeeded=%v", err, succeeded)
	}
}
