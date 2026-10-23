package downloader

import "testing"

func TestMonitorDetectsAllRemainingWorkersFailed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states []StatusCode
		want   bool
	}{
		{"empty", nil, false},
		{"completed", []StatusCode{StatusCodeSucceeded, StatusCodeCanceled}, false},
		{"failed", []StatusCode{StatusCodeFailed}, true},
		{"all_failure_types", []StatusCode{StatusCodeFailed, StatusCodeNetError, StatusCodeInternalError, StatusCodeTooManyConnections}, true},
		{"completed_and_failed", []StatusCode{StatusCodeSucceeded, StatusCodeNetError, StatusCodeCanceled}, true},
		{"active", []StatusCode{StatusCodeFailed, StatusCodeDownloading}, false},
		{"pending", []StatusCode{StatusCodeSucceeded, StatusCodePending, StatusCodeFailed}, false},
		{"initializing", []StatusCode{StatusCodeInit, StatusCodeFailed}, false},
		{"writing", []StatusCode{StatusCodeFailed, StatusCodeWaitToWrite}, false},
		{"paused", []StatusCode{StatusCodePaused, StatusCodeFailed}, false},
		{"reset", []StatusCode{StatusCodeFailed, StatusCodeReseted}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := NewMonitor()
			for _, state := range tc.states {
				worker := NewWorker(0, "", nil)
				worker.status.SetStatusCode(state)
				mt.Append(worker)
			}
			if got := mt.IsLeftWorkersAllFailed(); got != tc.want {
				t.Fatalf("all remaining workers failed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMonitorDoesNotReloadFatalWorker(t *testing.T) {
	mt := NewMonitor()
	mt.resetController = NewResetController(1)
	worker := NewWorker(0, "", nil)
	worker.lazyInit()
	worker.status.SetStatusCode(StatusCodeInternalError)
	mt.ResetWorker(worker)
	if !mt.resetController.CanReset() {
		t.Fatal("fatal worker consumed retry budget")
	}
	if worker.GetStatus().StatusCode() != StatusCodeInternalError {
		t.Fatal("fatal status was cleared before the monitor could report it")
	}
}
