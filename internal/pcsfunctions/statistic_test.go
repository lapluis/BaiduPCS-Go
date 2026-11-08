package pcsfunctions

import (
	"runtime"
	"sync"
	"testing"
)

func TestStatisticConcurrentTotalSize(t *testing.T) {
	const writers, readers, samples = 4, 4, 4096
	const bytesPerFile = int64(1)<<32 + 1
	const expected = writers * samples * bytesPerFile
	var statistic Statistic
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < samples; j++ {
				statistic.AddTotalSize(bytesPerFile)
				if j%64 == 0 {
					runtime.Gosched()
				}
			}
		}()
	}
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var previous int64
			for j := 0; j < samples; j++ {
				current := statistic.TotalSize()
				if current < previous || current > expected || current%bytesPerFile != 0 {
					t.Errorf("invalid observed total: previous=%d current=%d", previous, current)
					return
				}
				previous = current
				if j%64 == 0 {
					runtime.Gosched()
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := statistic.TotalSize(); got != expected {
		t.Fatalf("completed byte total = %d, want %d", got, expected)
	}
}
