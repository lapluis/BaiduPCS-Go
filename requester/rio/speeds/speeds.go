// Package speeds provides transfer speed statistics.
package speeds

import (
	"math"
	"math/bits"
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

// Add accounts for transferred bytes, saturating at the representable maximum.
// Non-positive counts do not subtract bytes from a sample.
func (sps *Speeds) Add(count int64) {
	sps.mu.Lock()
	defer sps.mu.Unlock()
	sps.initLocked()
	if count <= 0 {
		return
	}
	if count > math.MaxInt64-sps.count {
		sps.count = math.MaxInt64
	} else {
		sps.count += count
	}
}

// GetSpeeds returns the rate for the configured interval and starts a new
// sample once the interval elapses. Sampling and byte accounting are atomic.
func (sps *Speeds) GetSpeeds() int64 {
	sps.mu.Lock()
	defer sps.mu.Unlock()
	sps.initLocked()
	return sps.sampleLocked(time.Now())
}

// sampleLocked keeps the exact integer ratio until the final division. The
// public signed result saturates rather than overflowing or rounding via float.
func (sps *Speeds) sampleLocked(now time.Time) int64 {
	since := now.Sub(sps.nowTime)
	if since <= 0 {
		return 0
	}
	hi, lo := bits.Mul64(uint64(sps.count), uint64(sps.interval))
	rate := int64(math.MaxInt64)
	if hi < uint64(since) {
		quotient, _ := bits.Div64(hi, lo, uint64(since))
		if quotient <= math.MaxInt64 {
			rate = int64(quotient)
		}
	}
	if since >= sps.interval {
		sps.count = 0
		sps.nowTime = now
	}
	return rate
}
