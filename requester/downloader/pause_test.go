package downloader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestPauseTerminalWorkerReturnsImmediately(t *testing.T) {
	for _, state := range []StatusCode{StatusCodeSucceeded, StatusCodeCanceled, StatusCodeInternalError} {
		worker := NewWorker(0, "", nil)
		worker.SetAcceptRange("bytes")
		worker.SetRange(&transfer.Range{Begin: 0, End: 8})
		worker.status.SetStatusCode(state)
		done := make(chan struct{})
		go func() { worker.Pause(); close(done) }()
		select {
		case <-done:
			if worker.GetStatus().StatusCode() != state {
				t.Errorf("pause changed terminal state %v", state)
			}
		case <-time.After(250 * time.Millisecond):
			t.Errorf("pause blocked for terminal state %v", state)
		}
	}
}

func TestPauseInterruptsPendingRequestAndResumes(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-r.Context().Done():
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Range", "bytes 0-7/8")
		w.Header().Set("Content-Length", "8")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("01234567"))
	}))
	defer server.Close()
	defer close(release)
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, server.URL, f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	done := make(chan struct{})
	go func() { worker.Execute(); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	paused := make(chan struct{})
	go func() { worker.Pause(); close(paused) }()
	select {
	case <-paused:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("pause blocked waiting for response headers")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("pause left request running")
	}
	if worker.GetStatus().StatusCode() != StatusCodePaused {
		t.Fatalf("paused worker state: %v", worker.GetStatus().StatusCode())
	}
	worker.Pause()
	worker.Resume()
	worker.runWG.Wait()
	got, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "01234567" || worker.Err() != nil {
		t.Fatalf("resume: %q, %v", got, worker.Err())
	}
}

func TestPausedMonitorDoesNotAssignNewRanges(t *testing.T) {
	mt := NewMonitor()
	mt.lazyInit()
	gen := transfer.NewRangeListGenBlockSize(32, 8, 8)
	mt.status.SetTotalSize(32)
	mt.status.SetRangeListGen(gen)
	worker := NewWorker(0, "", nil)
	worker.SetRange(&transfer.Range{Begin: 8, End: 8})
	worker.SetAcceptRange("bytes")
	worker.status.SetStatusCode(StatusCodeSucceeded)
	mt.Append(worker)
	mt.Pause()
	mt.TryAddNewWork()
	if gen.LoadBegin() != 8 || worker.GetRange().LoadEnd() != 8 {
		t.Fatal("paused monitor scheduled a new block on an idle worker")
	}
}

func TestDownloaderPauseResumeKeepsTaskRunning(t *testing.T) {
	var calls atomic.Int32
	started, interrupted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-r.Context().Done():
				close(interrupted)
			case <-release:
			}
			return
		}
		w.Header().Set("Content-Range", "bytes 0-7/8")
		w.Header().Set("Content-Length", "8")
		w.WriteHeader(http.StatusPartialContent)
		w.Write([]byte("01234567"))
	}))
	defer server.Close()
	defer close(release)
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 8192})
	d.SetFileContentLength(8)
	defer d.Cancel()
	done := make(chan error, 1)
	go func() { done <- d.Execute() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	d.Pause()
	select {
	case <-interrupted:
	case <-time.After(time.Second):
		t.Fatal("paused request did not stop")
	}
	select {
	case err := <-done:
		t.Fatalf("pause terminated task: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	d.Resume()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("resumed task did not complete")
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "01234567" {
		t.Fatalf("resumed data: %q, %v", got, err)
	}
}
