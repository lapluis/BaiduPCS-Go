package speeds

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestRateLimitRefillDebt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rate    int64
		debt    uint64
		elapsed time.Duration
		want    uint64
		periods int64
	}{
		{"partial", 100, 350, time.Second / 2, 350, 0},
		{"stale", 100, 350, -time.Second, 350, 0},
		{"one", 100, 350, time.Second, 250, 1},
		{"dropped-ticks", 100, 350, 3400 * time.Millisecond, 50, 3},
		{"paid", 100, 350, 4 * time.Second, 0, 4},
		{"exact", 100, 300, 3 * time.Second, 0, 3},
		{"idle", 100, 0, 10 * time.Second, 0, 10},
		{"unsigned", math.MaxInt64, math.MaxUint64 - 2, time.Second, math.MaxInt64 - 1, 1},
		{"tiny-rate", 1, math.MaxUint64 - 2, time.Second, math.MaxUint64 - 3, 1},
		{"disabled", 0, 350, time.Second, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(100, 0)
			r := RateLimit{MaxRate: tc.rate, count: tc.debt, interval: time.Second, lastRefill: start, muChan: make(chan struct{})}
			old := r.muChan
			r.refillLocked(start.Add(tc.elapsed))
			if r.count != tc.want || !r.lastRefill.Equal(start.Add(time.Duration(tc.periods)*time.Second)) {
				t.Fatalf("debt=%d phase=%v", r.count, r.lastRefill)
			}
			select {
			case <-old:
				if tc.periods == 0 {
					t.Fatal("early wake")
				}
			default:
				if tc.periods > 0 {
					t.Fatal("missing wake")
				}
			}
		})
	}
}

func TestRateLimitRefillKeepsFractionAndAvoidsOverflow(t *testing.T) {
	start := time.Unix(100, 0)
	r := RateLimit{MaxRate: 100, count: 350, interval: time.Second, lastRefill: start, muChan: make(chan struct{})}
	for i, elapsed := range []time.Duration{1500 * time.Millisecond, 1900 * time.Millisecond, 2100 * time.Millisecond} {
		r.refillLocked(start.Add(elapsed))
		if r.count != []uint64{250, 250, 150}[i] {
			t.Fatal(r.count)
		}
	}
	r = RateLimit{MaxRate: math.MaxInt64, count: math.MaxUint64 - 2, interval: time.Nanosecond, lastRefill: start, muChan: make(chan struct{})}
	r.refillLocked(start.Add(time.Duration(math.MaxInt64)))
	if r.count != 0 {
		t.Fatal("overflow retained debt", r.count)
	}
}

func TestRateLimitElapsedAdmissionAndIntervalChange(t *testing.T) {
	r := NewRateLimit(10)
	r.SetInterval(time.Hour)
	defer r.Stop()
	r.Add(1000)
	r.mu.Lock()
	r.lastRefill = time.Now().Add(-3*time.Hour - 30*time.Minute)
	r.mu.Unlock()
	r.SetInterval(2 * time.Hour)
	r.mu.Lock()
	debt := r.count
	r.lastRefill = time.Now().Add(-200 * time.Hour)
	r.mu.Unlock()
	if debt != 970 {
		t.Fatal("interval change discarded debt", debt)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.AddWithContext(ctx, 1); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count != 1 {
		t.Fatal(r.count)
	}
}

func TestRateLimitContextDoesNotStopSharedLimiter(t *testing.T) {
	r := NewRateLimit(1)
	r.SetInterval(time.Hour)
	defer r.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.AddWithContext(ctx, 1); !errors.Is(err, context.Canceled) || r.ticker != nil {
		t.Fatal("pre-canceled admission", err)
	}
	r.Add(1000)
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := r.AddWithContext(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	r.mu.Lock()
	debt, stopped := r.count, r.stopped
	r.mu.Unlock()
	if debt != 1000 || stopped {
		t.Fatal("canceled waiter changed shared limiter", debt, stopped)
	}
}
