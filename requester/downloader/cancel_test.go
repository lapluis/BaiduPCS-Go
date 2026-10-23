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
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
)

type cancelSignalBody struct {
	io.ReadCloser
	started chan struct{}
	once    sync.Once
}

func (b *cancelSignalBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	return b.ReadCloser.Read(p)
}

func TestCancelWhileReadingInitialProbeBody(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "32")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: f.Name() + ".state"})
	d.SetDURLCheckFunc(func(client *requester.HTTPClient, url string) (int64, *http.Response, error) {
		resp, err := client.Req(http.MethodGet, url, nil, nil)
		if err == nil {
			resp.Body = &cancelSignalBody{ReadCloser: resp.Body, started: started}
		}
		return 32, resp, err
	})
	done := make(chan error, 1)
	go func() { done <- d.Execute() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("initial response read did not start")
	}
	d.Cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel initial response: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("initial response read was not canceled")
	}
}

func TestCanceledDownloadRetainsProgressAndStopsWorkers(t *testing.T) {
	for _, phase := range []string{"before_start", "response_headers", "response_body", "rate_limit"} {
		t.Run(phase, func(t *testing.T) {
			started, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "response_body" || phase == "rate_limit" {
					w.Header().Set("Content-Range", "bytes 8-31/32")
					w.Header().Set("Content-Length", "24")
					w.WriteHeader(http.StatusPartialContent)
					if phase == "rate_limit" {
						w.Write(bytes.Repeat([]byte("a"), 24))
					}
					w.(http.Flusher).Flush()
				}
				close(started)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			dir := t.TempDir()
			filename, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
			partial := []byte("01234567")
			if err := os.WriteFile(filename, partial, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte(`{"total_size":32,"gen_begin":32,"ranges":[{"begin":8,"end":32}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(filename, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath})
			d.SetFileContentLength(32)
			succeeded := false
			d.OnSuccess(func() { succeeded = true })
			if phase == "before_start" {
				d.OnExecute(d.Cancel)
			}
			if phase == "rate_limit" {
				rl := speeds.NewRateLimit(1)
				rl.SetInterval(time.Hour)
				rl.Add(1)
				defer rl.Stop()
				d.OnExecute(func() { d.monitor.status.SetRateLimit(rl) })
			}
			done := make(chan error, 1)
			go func() { done <- d.Execute() }()
			if phase != "before_start" {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("worker request did not start")
				}
				d.Cancel()
			}
			select {
			case err = <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("canceled download did not stop")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled download returned %v", err)
			}
			if succeeded {
				t.Error("canceled download fired success callback")
			}
			if _, err := os.Stat(statePath); err != nil {
				t.Errorf("cancellation removed resume state: %v", err)
			}
			got, err := os.ReadFile(filename)
			if err != nil || (phase != "rate_limit" && !bytes.Equal(got, partial)) {
				t.Errorf("cancellation changed partial file: %q, %v", got, err)
			}
			select {
			case <-d.monitor.CompletedChan():
			default:
				t.Error("cancellation left monitor completion channel open")
			}
			if phase != "before_start" && phase != "rate_limit" {
				select {
				case <-canceled:
				case <-time.After(time.Second):
					t.Error("cancellation left worker HTTP request active")
				}
			}
		})
	}
}
