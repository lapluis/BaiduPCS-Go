package downloader

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupPauseResumeKeepsWorkersUnderTaskContext(t *testing.T) {
	started, interrupted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	startOnce, interruptOnce := sync.Once{}, sync.Once{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
			interruptOnce.Do(func() { close(interrupted) })
		case <-release:
		}
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024})
	d.SetFileContentLength(8)
	d.OnExecute(func() {
		d.Pause()
		d.Resume()
		select {
		case <-started:
			t.Error("resume started a request before the startup callback returned")
		case <-time.After(50 * time.Millisecond):
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result error
	go func() { result = d.ExecuteWithContext(ctx); close(done) }()
	defer func() {
		cancel()
		close(release)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("download did not terminate during cleanup")
		}
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start its request")
	}
	cancel()
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Error("startup resume left a request outside the task cancellation context")
	}
	select {
	case <-done:
		if !errors.Is(result, context.Canceled) {
			t.Errorf("cancellation result: %v", result)
		}
	case <-time.After(time.Second):
		t.Error("canceled task did not join the startup worker")
	}
}

func TestStartupPauseResumeDownloadsExactlyOnce(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024})
	d.SetFileContentLength(8)
	d.OnExecute(func() {
		for i := 0; i < 3; i++ {
			d.Pause()
			d.Resume()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "abcdefgh" || requests.Load() != 1 || d.monitor.Status().Downloaded() != 8 {
		t.Fatalf("startup resume result: data=%q requests=%d downloaded=%d err=%v", got, requests.Load(), d.monitor.Status().Downloaded(), err)
	}
}
