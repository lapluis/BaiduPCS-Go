package downloader

import (
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

func TestCheckpointFailureStopsMonitorAndPreservesCause(t *testing.T) {
	for _, cancelTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "periodic", true: "canceled"}[cancelTask], func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Range", "bytes 0-7/8")
				w.Header().Set("Content-Length", "8")
				w.WriteHeader(206)
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
				close(stopped)
			}))
			defer server.Close()
			dir := t.TempDir()
			statePath := filepath.Join(dir, "state")
			if err := os.WriteFile(statePath, []byte("keep old checkpoint"), 0600); err != nil {
				t.Fatal(err)
			}
			stateFile, err := os.Open(statePath) // Read-only: saving must fail.
			if err != nil {
				t.Fatal(err)
			}
			defer stateFile.Close()
			f, err := os.Create(filepath.Join(dir, "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			worker := NewWorker(0, server.URL, f)
			worker.SetAcceptRange("bytes")
			worker.SetTotalSize(8)
			worker.SetRange(&transfer.Range{Begin: 0, End: 8})
			monitor := NewMonitor()
			monitor.Append(worker)
			status := transfer.NewDownloadStatus()
			status.SetTotalSize(8)
			status.SetRangeListGen(transfer.NewRangeListGenDefault(8, 8, 1, 1))
			monitor.SetStatus(status)
			state := NewInstanceState(stateFile, InstanceStateStorageFormatJSON)
			defer state.Close()
			monitor.SetInstanceState(state)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() { monitor.Execute(ctx); close(done) }()
			select {
			case <-started:
			case <-time.After(time.Second):
				cancel()
				<-done
				t.Fatal("range did not start")
			}
			if cancelTask {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("checkpoint failure did not stop monitor")
			}
			var pathErr *os.PathError
			if !errors.Is(monitor.Err(), ErrCheckpointSave) || !errors.As(monitor.Err(), &pathErr) {
				t.Fatalf("checkpoint write error hidden: %v", monitor.Err())
			}
			if cancelTask && !errors.Is(monitor.Err(), context.Canceled) {
				t.Fatal("cancellation cause lost")
			}
			if !cancelTask && errors.Is(monitor.Err(), context.DeadlineExceeded) {
				t.Fatal("checkpoint failure waited for network timeout")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("checkpoint failure left HTTP request live")
			}
			got, err := os.ReadFile(statePath)
			if err != nil || string(got) != "keep old checkpoint" {
				t.Fatal("failed truncate changed old checkpoint", err)
			}
		})
	}
}
