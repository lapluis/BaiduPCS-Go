package speeds_test

import (
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"testing"
	"time"
)

func TestRateLimitStopReleasesWaiters(t *testing.T) {
	limiter := speeds.NewRateLimit(1)
	limiter.SetInterval(time.Hour)
	limiter.Add(1)
	done := make(chan struct{})
	go func() { limiter.Add(1); close(done) }()
	select {
	case <-done:
		t.Fatal("limit did not block")
	case <-time.After(20 * time.Millisecond):
	}
	limiter.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop left Add blocked")
	}
}
func TestRateLimitStopIsIdempotent(t *testing.T) {
	limiter := speeds.NewRateLimit(1)
	limiter.Add(1)
	limiter.Stop()
	limiter.Stop()
}
func TestRateLimitStopBeforeStart(t *testing.T) {
	limiter := speeds.NewRateLimit(1)
	limiter.SetInterval(time.Hour)
	limiter.Stop()
	done := make(chan struct{})
	go func() { limiter.Add(1); limiter.Add(1); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stopped limiter restarted")
	}
}

func TestRateLimitIntervalChangeReleasesWaiter(t *testing.T) {
	limiter := speeds.NewRateLimit(1)
	defer limiter.Stop()
	limiter.SetInterval(time.Hour)
	limiter.Add(1)
	done := make(chan struct{})
	go func() { limiter.Add(1); close(done) }()
	select {
	case <-done:
		t.Fatal("limit bypassed")
	case <-time.After(20 * time.Millisecond):
	}
	limiter.SetInterval(5 * time.Millisecond)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("interval update stranded waiter")
	}
}

func TestRateLimitConcurrentStop(t *testing.T) {
	limiter := speeds.NewRateLimit(1)
	limiter.SetInterval(time.Millisecond)
	limiter.Add(1)
	done := make(chan struct{}, 16)
	for i := 0; i < 16; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				limiter.Add(1)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		limiter.SetInterval(time.Duration(i+1) * time.Millisecond)
	}
	limiter.Stop()
	limiter.SetInterval(time.Millisecond)
	limiter.Stop()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for i := 0; i < 16; i++ {
		select {
		case <-done:
		case <-timer.C:
			t.Fatal("concurrent waiters stranded")
		}
	}
}

func TestRateLimitDisabled(t *testing.T) {
	for _, rate := range []int64{0, -1} {
		limiter := speeds.NewRateLimit(rate)
		done := make(chan struct{})
		go func() { limiter.Add(100); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			limiter.Stop()
			t.Fatal("disabled limiter blocked")
		}
		limiter.Stop()
	}
}
