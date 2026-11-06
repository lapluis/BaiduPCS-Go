package downloader

import (
	"context"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestCanceledPendingRangeCannotBeReassigned(t *testing.T) {
	monitor := NewMonitor()
	monitor.lazyInit()
	gen := transfer.NewRangeListGenBlockSize(32, 16, 8)
	monitor.status.SetTotalSize(32)
	monitor.status.SetRangeListGen(gen)
	worker := NewWorker(0, "", nil)
	worker.SetAcceptRange("bytes")
	worker.SetRange(&transfer.Range{Begin: 8, End: 16})
	worker.SetTotalSize(32)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker.parentCtx = ctx
	worker.status.SetStatusCode(StatusCodeCanceled)
	monitor.Append(worker)
	if monitor.GetAvailableWorker() != nil {
		t.Error("canceled range with pending bytes selected as an idle worker")
	}
	monitor.TryAddNewWork()
	worker.runWG.Wait()
	if gen.LoadBegin() != 16 || worker.GetRange().LoadBegin() != 8 || worker.GetRange().LoadEnd() != 16 {
		t.Errorf("pending range overwritten: generator=%d range=%s", gen.LoadBegin(), worker.GetRange().ShowDetails())
	}
	checkpoint := &transfer.DownloadInstanceInfoExport{}
	checkpoint.SetInstanceInfo(&transfer.DownloadInstanceInfo{DownloadStatus: monitor.status, Ranges: monitor.GetAllWorkersRange()})
	if got := checkpoint.GetInstanceInfo().DownloadStatus.Downloaded(); got != 8 {
		t.Fatalf("checkpoint falsely certifies missing bytes: downloaded=%d want=8", got)
	}
}

func TestAvailableWorkerRequiresNoRemainingBytes(t *testing.T) {
	for _, state := range []StatusCode{StatusCodeSucceeded, StatusCodeCanceled} {
		for _, pending := range []bool{false, true} {
			monitor := NewMonitor()
			worker := NewWorker(0, "", nil)
			end := int64(8)
			if pending {
				end = 16
			}
			worker.SetAcceptRange("bytes")
			worker.SetRange(&transfer.Range{Begin: 8, End: end})
			worker.status.SetStatusCode(state)
			monitor.Append(worker)
			if got := monitor.GetAvailableWorker(); (got != nil) == pending {
				t.Errorf("state=%v pending=%v incorrectly classified as idle=%v", state, pending, got != nil)
			}
		}
	}
}
