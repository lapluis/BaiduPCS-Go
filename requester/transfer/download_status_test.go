package transfer

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadTimeLeftBounds(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, done, rate int64
		want              time.Duration
	}{
		{"slow-large-file", 128 << 30, 0, 1, -1},
		{"extreme-size", math.MaxInt64, 0, 1, -1},
		{"unknown-size", -1, 10, 2, -1},
		{"complete", 10, 10, 2, 0},
		{"over-complete", 10, 20, 2, 0},
		{"stalled", 10, 7, 0, -1},
		{"whole-seconds", 10, 7, 2, time.Second},
		{"resumed", 128 << 30, (128 << 30) - 768, 128, 6 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewDownloadStatus()
			s.SetTotalSize(tc.total)
			s.AddDownloaded(tc.done)
			atomic.StoreInt64(&s.tmpSpeeds, tc.rate)
			if got := s.TimeLeft(); got != tc.want {
				t.Fatalf("TimeLeft()=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestConcurrentDownloadStatusReadsAreAtomic(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	const a, b = int64(1<<32 - 1), int64(1 << 32)
	for _, field := range []string{"total", "remaining-time"} {
		t.Run(field, func(t *testing.T) {
			status := NewDownloadStatus()
			status.SetTotalSize(a)
			read := status.TotalSize
			write := func() { status.SetTotalSize(b); status.SetTotalSize(a) }
			valid := func(v int64) bool { return v == a || v == b }
			if field == "remaining-time" {
				status.SetTotalSize(1 << 33)
				status.AddDownloaded(a)
				atomic.StoreInt64(&status.tmpSpeeds, 1<<30)
				read = func() int64 { return int64(status.TimeLeft()) }
				write = func() { status.AddDownloaded(1); status.AddDownloaded(-1) }
				valid = func(v int64) bool { return v == int64(4*time.Second) }
			}
			var stop, invalid atomic.Bool
			var reads, bad atomic.Int64
			var ready, finished sync.WaitGroup
			start := make(chan struct{})
			ready.Add(4)
			finished.Add(4)
			for i := 0; i < 3; i++ {
				go func() {
					defer finished.Done()
					ready.Done()
					<-start
					for !stop.Load() {
						v := read()
						reads.Add(1)
						if !valid(v) {
							bad.Store(v)
							invalid.Store(true)
							stop.Store(true)
						}
					}
				}()
			}
			go func() {
				defer finished.Done()
				defer stop.Store(true)
				ready.Done()
				<-start
				for i := 0; i < 300000 && !stop.Load(); i++ {
					write()
				}
			}()
			ready.Wait()
			close(start)
			finished.Wait()
			if invalid.Load() || reads.Load() == 0 {
				t.Fatalf("torn %s value: value=%d observations=%d", field, bad.Load(), reads.Load())
			}
		})
	}
}
