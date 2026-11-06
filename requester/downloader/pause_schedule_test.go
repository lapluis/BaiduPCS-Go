package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestPauseSerializesWithPendingRangeAssignment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	monitor := NewMonitor()
	monitor.lazyInit()
	monitor.status.SetTotalSize(32)
	monitor.status.SetRangeListGen(transfer.NewRangeListGenBlockSize(32, 8, 8))
	worker := NewWorker(0, server.URL, nil)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(32)
	worker.SetRange(&transfer.Range{Begin: 8, End: 8})
	worker.status.SetStatusCode(StatusCodeSucceeded)
	worker.parentCtx = ctx
	monitor.Append(worker)
	defer func() { cancel(); worker.runWG.Wait() }()
	// Stop assignment after its initial paused check, before it can reuse
	// the idle worker. Pause must cover that assignment before returning.
	monitor.resetController.mu.Lock()
	release := sync.OnceFunc(monitor.resetController.mu.Unlock)
	defer release()
	scheduled := make(chan struct{})
	go func() { monitor.TryAddNewWork(); close(scheduled) }()
	select {
	case <-scheduled:
		t.Fatal("assignment did not wait for retry budget")
	case <-time.After(50 * time.Millisecond):
	}
	paused := make(chan struct{})
	go func() { monitor.Pause(); close(paused) }()
	select {
	case <-paused:
	case <-time.After(50 * time.Millisecond):
	}
	release()
	<-scheduled
	<-paused
	worker.controlMu.Lock()
	workerPaused := worker.paused
	worker.controlMu.Unlock()
	if worker.GetRange().Len() > 0 && !workerPaused {
		t.Fatalf("pause returned before newly assigned range was paused: status=%v range=%s", worker.GetStatus().StatusCode(), worker.GetRange().ShowDetails())
	}
}

func TestRangeWorkerCallbackCanPauseMonitor(t *testing.T) {
	monitor := NewMonitor()
	monitor.Append(NewWorker(0, "", nil))
	done := make(chan struct{})
	go func() {
		monitor.RangeWorker(func(_ int, _ *Worker) bool { monitor.Pause(); return true })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("range callback blocked while pausing the monitor")
	}
}

func TestRangeWorkerUsesStableSnapshot(t *testing.T) {
	monitor := NewMonitor()
	workers := WorkerList{NewWorker(0, "", nil), NewWorker(1, "", nil), NewWorker(2, "", nil)}
	monitor.SetWorkers(workers)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var visited []int
	go func() {
		monitor.RangeWorker(func(index int, worker *Worker) bool {
			if index == 0 {
				close(started)
				<-release
			}
			visited = append(visited, worker.ID())
			return true
		})
		close(done)
	}()
	<-started
	monitor.SetWorkers(WorkerList{workers[2], workers[1], workers[0]})
	unblock()
	<-done
	if len(visited) != 3 || visited[0] != 0 || visited[1] != 1 || visited[2] != 2 {
		t.Fatalf("worker replacement changed callback iteration: %v", visited)
	}
}
