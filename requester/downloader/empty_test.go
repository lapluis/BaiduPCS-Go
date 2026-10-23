package downloader

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestEmptyDownloadCompletesAndCleansState(t *testing.T) {
	for _, existing := range []string{"", "old content"} {
		t.Run(existing, func(t *testing.T) {
			dir := t.TempDir()
			filename, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
			if err := os.WriteFile(filename, []byte(existing), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.OpenFile(filename, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			d := NewDownloader("", f, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath})
			d.SetFileContentLength(0)
			executed, succeeded, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			d.OnExecute(func() { close(executed) })
			d.OnSuccess(func() { close(succeeded) })
			d.OnFinish(func() { close(finished) })
			err = d.Execute()
			f.Close()
			got, readErr := os.ReadFile(filename)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err != nil || len(got) != 0 {
				t.Fatalf("empty download: %q, %v", got, err)
			}
			if _, err := os.Stat(statePath); !os.IsNotExist(err) {
				t.Fatalf("completed download retained state: %v", err)
			}
			for _, event := range []<-chan struct{}{executed, succeeded, finished} {
				select {
				case <-event:
				case <-time.After(time.Second):
					t.Fatal("download lifecycle event not delivered")
				}
			}
			select {
			case <-d.monitor.CompletedChan():
			default:
				t.Fatal("empty download did not close completion channel")
			}
		})
	}
}

func TestCanceledEmptyMonitorDoesNotReportSuccess(t *testing.T) {
	mt := NewMonitor()
	mt.lazyInit()
	mt.status.SetTotalSize(0)
	mt.status.SetRangeListGen(transfer.NewRangeListGenDefault(0, 0, 0, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mt.Execute(ctx)
	if !errors.Is(mt.Err(), context.Canceled) {
		t.Fatalf("canceled empty monitor returned %v", mt.Err())
	}
}

func TestHTTPEmptyDownloadDoesNotRequestRanges(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			t.Error("empty download requested a range")
		}
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := NewDownloader(server.URL, f, NewConfig()).Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestMonitorWithoutWorkersStillRejectsUnconfiguredWork(t *testing.T) {
	mt := NewMonitor()
	mt.Execute(context.Background())
	if !errors.Is(mt.Err(), ErrNoWokers) {
		t.Fatalf("unexpected monitor error: %v", mt.Err())
	}
}

func TestDownloadRemovesOldTailOnlyAfterSuccess(t *testing.T) {
	content := []byte("new")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
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
	if err := NewDownloader(server.URL, f, NewConfig()).Execute(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("download retained old tail: %q, %v", got, err)
	}
}

type truncateFailureWriter struct {
	*os.File
	failure error
}

func (w truncateFailureWriter) Truncate(int64) error { return w.failure }

func TestEmptyDownloadKeepsStateWhenFinalSizeCannotBeSet(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	statePath := f.Name() + ".state"
	failure := errors.New("synthetic truncate failure")
	d := NewDownloader("", truncateFailureWriter{File: f, failure: failure}, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath})
	d.SetFileContentLength(0)
	if err := d.Execute(); !errors.Is(err, failure) {
		t.Fatalf("finalize error: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("failed finalization removed resume state: %v", err)
	}
}
