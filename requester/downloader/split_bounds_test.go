package downloader

import (
	"context"
	"math"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestDynamicSplitPreservesLargeOffsetIntervals(t *testing.T) {
	threshold := MinParallelSize / 5
	for _, tc := range []struct {
		name       string
		begin, end int64
		split      bool
	}{
		{"ordinary", 0, 8 * MinParallelSize, true},
		{"whole-int64-range", 0, math.MaxInt64, true},
		{"above-half-limit", math.MaxInt64/2 + 1, math.MaxInt64/2 + 1 + 8*MinParallelSize, true},
		{"near-limit", math.MaxInt64 - 8*MinParallelSize, math.MaxInt64, true},
		{"odd-large-range", math.MaxInt64 - 8*MinParallelSize - 1, math.MaxInt64, true},
		{"exact-minimum-right", math.MaxInt64 - (2*threshold - 1), math.MaxInt64, true},
		{"below-minimum-right", math.MaxInt64 - (2*threshold - 2), math.MaxInt64, false},
		{"complete", math.MaxInt64, math.MaxInt64, false},
		{"unknown-end", 0, -2, false},
		{"negative-begin", -8 * MinParallelSize, 8 * MinParallelSize, false},
		{"reversed", 8 * MinParallelSize, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := NewWorker(0, "", nil)
			worker.SetRange(&transfer.Range{Begin: tc.begin, End: tc.end})
			worker.status.SetStatusCode(StatusCodeDownloading)
			idle := NewWorker(1, "", nil)
			idle.SetRange(&transfer.Range{Begin: tc.end, End: tc.end})
			idle.status.SetStatusCode(StatusCodeSucceeded)
			// A regression must not open a connection or access a huge file.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			idle.parentCtx = ctx
			monitor := NewMonitor()
			monitor.lazyInit()
			monitor.resetController = NewResetController(1)
			monitor.Append(worker)
			monitor.Append(idle)
			monitor.DynamicSplitWorker(worker)
			idle.runWG.Wait()
			if !tc.split {
				if worker.GetRange().LoadBegin() != tc.begin || worker.GetRange().LoadEnd() != tc.end || idle.GetRange().LoadBegin() != tc.end || idle.GetRange().LoadEnd() != tc.end || !monitor.resetController.CanReset() {
					t.Fatalf("unsplittable interval changed: source=%s idle=%s", worker.GetRange().ShowDetails(), idle.GetRange().ShowDetails())
				}
				return
			}
			middle := tc.begin + (tc.end-tc.begin)/2
			if worker.GetRange().LoadBegin() != tc.begin || worker.GetRange().LoadEnd() != middle || idle.GetRange().LoadBegin() != middle || idle.GetRange().LoadEnd() != tc.end {
				t.Fatalf("split interval wrong: source=%s idle=%s want midpoint=%d", worker.GetRange().ShowDetails(), idle.GetRange().ShowDetails(), middle)
			}
			if worker.GetRange().Len()+idle.GetRange().Len() != tc.end-tc.begin || monitor.resetController.CanReset() {
				t.Fatal("split lost bytes or failed to charge its connection budget")
			}
		})
	}
}
