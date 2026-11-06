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

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type cancelAfterCheckpointWrite struct {
	*failingSyncFile
	cancel context.CancelFunc
}

func (f *cancelAfterCheckpointWrite) WriteAt(p []byte, off int64) (int, error) {
	n, err := f.File.WriteAt(p, off)
	if n > 0 && f.cancel != nil {
		f.cancel()
	}
	return n, err
}

func TestCheckpointDataFlushFailureRetainsOlderProgress(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "periodic", true: "canceled"}[cancelTask], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Range", "bytes 0-2047/2048")
				w.Header().Set("Content-Length", "2048")
				w.WriteHeader(206)
				_, _ = w.Write(make([]byte, 1024))
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state")
			old := []byte(`{"total_size":2048,"gen_begin":2048,"ranges":[{"begin":0,"end":2048}]}`)
			if err := os.WriteFile(statePath, old, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Create(filepath.Join(dir, "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			wantErr := errors.New("injected payload flush failure")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			writer := &cancelAfterCheckpointWrite{failingSyncFile: &failingSyncFile{File: f, err: wantErr}}
			if cancelTask {
				// Cancel at the write itself: polling the file can lose the race
				// to the periodic checkpoint and test a different failure path.
				writer.cancel = cancel
			}
			d := NewDownloader(server.URL, writer, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath})
			d.SetFileContentLength(2048)
			done := make(chan error, 1)
			go func() { done <- d.ExecuteWithContext(ctx) }()
			deadline := time.Now().Add(time.Second)
			written := false
			for time.Now().Before(deadline) {
				info, err := f.Stat()
				if err == nil && info.Size() >= 1024 {
					written = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !written {
				cancel()
				<-done
				t.Fatal("partial payload not written")
			}
			select {
			case err := <-done:
				if !errors.Is(err, wantErr) || !errors.Is(err, ErrCheckpointSave) || !writer.called.Load() {
					t.Errorf("unflushed payload progress accepted: err=%v synced=%v", err, writer.called.Load())
				}
				if cancelTask && !errors.Is(err, context.Canceled) {
					t.Error("cancellation cause lost")
				}
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("data flush failure did not stop download")
			}
			got, err := os.ReadFile(statePath)
			if err != nil || string(got) != string(old) {
				t.Fatalf("checkpoint advanced beyond durable data: %q %v", got, err)
			}
		})
	}
}

type pausedDataFlush struct {
	*os.File
	started, release chan struct{}
}

func (f *pausedDataFlush) Sync() error {
	err := f.File.Sync()
	close(f.started)
	// Writes after the flush completed have no durability guarantee.
	<-f.release
	return err
}

func TestCheckpointDoesNotCertifyWritesAfterDataFlush(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stateFile, err := os.Create(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(stateFile, InstanceStateStorageFormatJSON)
	defer state.Close()
	writer := &pausedDataFlush{File: f, started: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(writer.release) })
	defer release()
	worker := NewWorker(0, "", writer)
	worker.SetWriteMutex(&sync.Mutex{})
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	status.SetRangeListGen(transfer.NewRangeListGenDefault(8, 8, 1, 1))
	worker.SetDownloadStatus(status)
	worker.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("12345678")))}
	monitor := NewMonitor()
	monitor.SetStatus(status)
	monitor.SetInstanceState(state)
	monitor.Append(worker)
	saved := make(chan error, 1)
	go func() { saved <- monitor.saveCheckpoint() }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("data flush did not start")
	}
	written := make(chan struct{})
	go func() { worker.Execute(); close(written) }()
	// Allow a writer to advance if checkpointing fails to guard its snapshot.
	select {
	case <-written:
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("checkpoint did not finish")
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("writer did not finish")
	}
	if worker.Err() != nil {
		t.Fatal(worker.Err())
	}
	loaded := state.Get()
	if loaded == nil || loaded.Ranges.Len() != 8 {
		t.Fatal("checkpoint certified data written after its flush")
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "12345678" {
		t.Fatal("writer fixture did not complete", err)
	}
}
