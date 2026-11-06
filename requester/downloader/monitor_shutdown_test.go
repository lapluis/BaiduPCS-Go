package downloader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestResumeCannotRestartWorkerAfterCanceledMonitorReturns(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	monitor := NewMonitor()
	monitor.lazyInit()
	monitor.status.SetTotalSize(8)
	monitor.status.SetRangeListGen(transfer.NewRangeListGenBlockSize(8, 8, 8))
	worker := NewWorker(0, server.URL, nil)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	monitor.Append(worker)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { monitor.Execute(ctx); close(done) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	monitor.Pause()
	worker.runWG.Wait()
	cancel()
	<-done
	if !errors.Is(monitor.Err(), context.Canceled) {
		t.Fatalf("unexpected completion error: %v", monitor.Err())
	}
	before := worker.status.StatusCode()
	monitor.Resume()
	worker.runWG.Wait()
	if worker.status.StatusCode() != before || !monitor.paused.Load() {
		t.Fatalf("resume changed a terminated task: before=%v after=%v paused=%v", before, worker.status.StatusCode(), monitor.paused.Load())
	}
}

func TestCanceledMonitorCannotAssignPendingRangesAfterReturn(t *testing.T) {
	monitor := NewMonitor()
	monitor.lazyInit()
	gen := transfer.NewRangeListGenBlockSize(16, 8, 8)
	monitor.status.SetTotalSize(16)
	monitor.status.SetRangeListGen(gen)
	worker := NewWorker(0, "", nil)
	worker.SetAcceptRange("bytes")
	worker.SetRange(&transfer.Range{Begin: 8, End: 8})
	worker.status.SetStatusCode(StatusCodeSucceeded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.parentCtx = ctx
	monitor.Append(worker)
	monitor.Execute(ctx)
	monitor.TryAddNewWork()
	worker.runWG.Wait()
	if gen.LoadBegin() != 8 || worker.GetRange().LoadBegin() != 8 || worker.GetRange().LoadEnd() != 8 {
		t.Fatalf("terminated monitor assigned more work: generated=%d range=%s", gen.LoadBegin(), worker.GetRange().ShowDetails())
	}
}
