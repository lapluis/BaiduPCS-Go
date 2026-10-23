package speeds

import (
	"sync"
	"time"
)

// RateLimit limits accounted bytes per interval. MaxRate must be configured
// before use; non-positive values disable limiting. Stop permanently releases
// waiters and is safe to call more than once.
type RateLimit struct {
	MaxRate   int64
	mu        sync.Mutex
	count     int64
	interval  time.Duration
	ticker    *time.Ticker
	muChan    chan struct{}
	closeChan chan struct{}
	stopped   bool
}

func NewRateLimit(maxRate int64) *RateLimit { return &RateLimit{MaxRate: maxRate} }

func (rl *RateLimit) SetInterval(i time.Duration) {
	if i <= 0 {
		i = time.Second
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.interval = i
	if rl.ticker != nil && !rl.stopped {
		rl.ticker.Reset(i)
	}
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
				rl.count = 0
				close(rl.muChan)
				rl.muChan = make(chan struct{})
				rl.mu.Unlock()
			case <-done:
				return
			}
		}
	}()
}

func (rl *RateLimit) Add(count int64) {
	if count <= 0 {
		return
	}
	for {
		rl.mu.Lock()
		if rl.stopped || rl.MaxRate <= 0 {
			rl.mu.Unlock()
			return
		}
		if rl.ticker == nil {
			rl.startLocked()
		}
		if rl.count < rl.MaxRate {
			// Preserve block accounting: the block that reaches the limit proceeds;
			// subsequent blocks wait for the next interval.
			rl.count += count
			rl.mu.Unlock()
			return
		}
		next, done := rl.muChan, rl.closeChan
		rl.mu.Unlock()
		select {
		case <-next:
		case <-done:
			return
		}
	}
}
