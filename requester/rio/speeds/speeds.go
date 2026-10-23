// Package speeds provides transfer speed statistics.
package speeds

import (
	"sync"
	"time"
)

type Speeds struct {
	count    int64
	interval time.Duration
	nowTime  time.Time
	mu       sync.Mutex
}

func (sps *Speeds) initLocked() {
	if sps.nowTime.IsZero() {
		sps.nowTime = time.Now()
	}
	if sps.interval <= 0 {
		sps.interval = time.Second
	}
}

// SetInterval sets the reporting interval; non-positive values are ignored.
func (sps *Speeds) SetInterval(interval time.Duration) {
	if interval <= 0 {
		return
	}
	sps.mu.Lock()
	defer sps.mu.Unlock()
	sps.interval = interval
}

// Add accounts for transferred bytes.
func (sps *Speeds) Add(count int64) {
	sps.mu.Lock()
	defer sps.mu.Unlock()
	sps.initLocked()
	sps.count += count
}

// GetSpeeds returns the rate for the configured interval and starts a new
// sample once the interval elapses. Sampling and byte accounting are atomic.
func (sps *Speeds) GetSpeeds() int64 {
	sps.mu.Lock()
	defer sps.mu.Unlock()
	sps.initLocked()
	now := time.Now()
	since := now.Sub(sps.nowTime)
	if since <= 0 {
		return 0
	}
	rate := int64(float64(sps.count) * sps.interval.Seconds() / since.Seconds())
	if since >= sps.interval {
		sps.count = 0
		sps.nowTime = now
	}
	return rate
}
