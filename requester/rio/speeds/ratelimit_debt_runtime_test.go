package speeds_test

import (
	"math"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
)

func TestRateLimitOversizedBlockDebtSurvivesTicks(t *testing.T) {
	r := speeds.NewRateLimit(1)
	defer r.Stop()
	r.SetInterval(time.Hour)
	r.Add(1 << 20)
	done := make(chan struct{})
	go func() { r.Add(1); close(done) }()
	r.SetInterval(time.Millisecond)
	select {
	case <-done:
		t.Fatal("one tick discarded the oversized block's debt")
	case <-time.After(40 * time.Millisecond):
	}
	r.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not release the debt waiter")
	}
}

func TestRateLimitAccountingCannotWrapNegative(t *testing.T) {
	r := speeds.NewRateLimit(math.MaxInt64)
	defer r.Stop()
	r.SetInterval(time.Hour)
	r.Add(math.MaxInt64 - 1)
	r.Add(math.MaxInt64)
	done := make(chan struct{})
	go func() { r.Add(1); close(done) }()
	select {
	case <-done:
		t.Fatal("overflowing block accounting bypassed the limit")
	case <-time.After(20 * time.Millisecond):
	}
	r.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("overflow-sized debt waiter did not stop")
	}
}
