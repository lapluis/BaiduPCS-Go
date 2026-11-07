package speeds

import (
	"context"
	"sync"
	"time"
)

// RateLimit limits accounted bytes per interval. One block may overshoot the
// allowance, but its excess remains debt across intervals. MaxRate must be
// configured before use; non-positive values disable limiting. Stop permanently
// releases waiters and is safe to call more than once.
type RateLimit struct {
	MaxRate    int64
	mu         sync.Mutex
	count      uint64
	lastRefill time.Time
	interval   time.Duration
	ticker     *time.Ticker
	muChan     chan struct{}
	closeChan  chan struct{}
	stopped    bool
}

func NewRateLimit(maxRate int64) *RateLimit { return &RateLimit{MaxRate: maxRate} }

func (rl *RateLimit) SetInterval(i time.Duration) {
	if i <= 0 {
		i = time.Second
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.ticker != nil && !rl.stopped {
		now := time.Now()
		rl.refillLocked(now)
		rl.lastRefill = now
		rl.ticker.Reset(i)
	}
	rl.interval = i
}

func (rl *RateLimit) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.stopped {
		return
	}
	rl.stopped = true
	if rl.ticker != nil {
		rl.ticker.Stop()
	}
	if rl.closeChan != nil {
		close(rl.closeChan)
	}
}

// startLocked runs once, while mu is held. The ticker's channel stays stable
// when SetInterval resets it, so waiters cannot be stranded on an old ticker.
func (rl *RateLimit) startLocked() {
	if rl.interval <= 0 {
		rl.interval = time.Second
	}
	rl.lastRefill = time.Now()
	rl.ticker = time.NewTicker(rl.interval)
	rl.closeChan = make(chan struct{})
	rl.muChan = make(chan struct{})
	ticks, done := rl.ticker.C, rl.closeChan
	go func() {
		for {
			select {
			case <-ticks:
				rl.mu.Lock()
				if rl.stopped {
					rl.mu.Unlock()
					return
				}
				rl.refillLocked(time.Now())
				rl.mu.Unlock()
			case <-done:
				return
			}
		}
	}()
}

// refillLocked accounts for all elapsed intervals, including dropped ticker
// notifications. Admission bounds count below MaxRate + one int64-sized block.
func (rl *RateLimit) refillLocked(now time.Time) {
	elapsed := now.Sub(rl.lastRefill)
	if elapsed < rl.interval {
		return
	}
	periods := uint64(elapsed / rl.interval)
	if rl.MaxRate <= 0 || periods > rl.count/uint64(rl.MaxRate) {
		rl.count = 0
	} else {
		rl.count -= periods * uint64(rl.MaxRate)
	}
	rl.lastRefill = rl.lastRefill.Add(time.Duration(periods) * rl.interval)
	close(rl.muChan)
	rl.muChan = make(chan struct{})
}

func (rl *RateLimit) Add(count int64) {
	_ = rl.AddWithContext(context.Background(), count)
}

// AddWithContext admits a block or cancels its wait without stopping the shared
// limiter. A canceled waiter is not charged; existing debt remains intact.
func (rl *RateLimit) AddWithContext(ctx context.Context, count int64) error {
	if count <= 0 {
		return ctx.Err()
	}
	for {
		rl.mu.Lock()
		if err := ctx.Err(); err != nil {
			rl.mu.Unlock()
			return err
		}
		if rl.stopped || rl.MaxRate <= 0 {
			rl.mu.Unlock()
			return nil
		}
		if rl.ticker == nil {
			rl.startLocked()
		}
		rl.refillLocked(time.Now())
		if rl.count < uint64(rl.MaxRate) {
			// Preserve block accounting: the block that reaches the limit proceeds;
			// subsequent blocks wait until the accumulated debt permits admission.
			rl.count += uint64(count)
			rl.mu.Unlock()
			return nil
		}
		next, done := rl.muChan, rl.closeChan
		rl.mu.Unlock()
		select {
		case <-next:
		case <-done:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
