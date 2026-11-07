package downloader

import (
	"context"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestMonitorDoesNotSpendBudgetOnDeclinedRestarts(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		state                       StatusCode
		failedLoop, previousContext bool
	}{
		{"initializing-with-previous-context", StatusCodeInit, false, true},
		{"failure-without-context", StatusCodeNetError, true, false},
		{"stalled-without-context", StatusCodeDownloading, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitor := NewMonitor()
			monitor.lazyInit()
			monitor.resetController = NewResetController(1)
			worker := NewWorker(0, "", nil)
			worker.SetAcceptRange("bytes")
			worker.SetRange(&transfer.Range{Begin: 0, End: 8})
			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Bound unexpected execution without opening a connection.
			worker.parentCtx = ctx
			resets := 0
			if tc.previousContext {
				worker.resetFunc = func() { resets++ }
			}
			worker.status.SetStatusCode(tc.state)
			monitor.Append(worker)
			if tc.failedLoop {
				monitor.ResetFailedAndNetErrorWorkers()
			} else {
				monitor.ResetWorker(worker)
			}
			worker.runWG.Wait()
			if !monitor.resetController.CanReset() || resets != 0 || worker.GetStatus().StatusCode() != tc.state || worker.GetRange().LoadBegin() != 0 {
				t.Fatalf("inactive or unresettable worker consumed budget: available=%t resets=%d status=%v want=%v", monitor.resetController.CanReset(), resets, worker.GetStatus().StatusCode(), tc.state)
			}
		})
	}
}
