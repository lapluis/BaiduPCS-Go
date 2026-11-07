package downloader

import "testing"

func TestWorkerSpeedIsReadableBeforeExecution(t *testing.T) {
	for name, worker := range map[string]*Worker{"constructor": NewWorker(0, "", nil), "zero-value": {}} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if failure := recover(); failure != nil {
					t.Fatalf("reading idle worker speed panicked: %v", failure)
				}
			}()
			for i := 0; i < 3; i++ {
				if rate := worker.GetSpeedsPerSecond(); rate != 0 {
					t.Fatalf("idle worker reported %d bytes/s", rate)
				}
			}
			if worker.client != nil || worker.GetRange() != nil || worker.GetStatus().StatusCode() != StatusCodeInit || worker.Err() != nil {
				t.Fatal("observing speed initialized or started the transfer")
			}
		})
	}
}
