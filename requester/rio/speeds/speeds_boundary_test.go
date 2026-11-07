package speeds

import (
	"math"
	"testing"
	"time"
)

func TestSpeedAccountingSaturatesInsteadOfWrapping(t *testing.T) {
	var s Speeds
	s.Add(math.MaxInt64)
	s.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count != math.MaxInt64 {
		t.Fatalf("byte count wrapped: %d", s.count)
	}
}

func TestSpeedShortSampleCannotBecomeNegative(t *testing.T) {
	var s Speeds
	s.SetInterval(time.Hour)
	s.Add(math.MaxInt64)
	s.mu.Lock()
	s.nowTime = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if rate := s.GetSpeeds(); rate != math.MaxInt64 {
		t.Fatalf("overflowed rate: %d", rate)
	}
}

func TestSpeedExactSamplingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count             int64
		interval, elapsed time.Duration
		want              int64
	}{
		{"empty", 0, time.Second, time.Second, 0},
		{"short", 1000, time.Second, time.Second / 2, 2000},
		{"late", 1000, time.Second, 2 * time.Second, 500},
		{"custom", 1000, time.Second / 4, 2 * time.Second, 125},
		{"truncate", 1, time.Second, 3 * time.Second, 0},
		{"float-precision", 1<<53 + 1, time.Second, time.Second, 1<<53 + 1},
		{"signed-limit", math.MaxInt64, time.Second, time.Second, math.MaxInt64},
		{"wide-product", math.MaxInt64, time.Duration(math.MaxInt64), time.Duration(math.MaxInt64), math.MaxInt64},
		{"large-denominator", math.MaxInt64, time.Nanosecond, time.Duration(math.MaxInt64), 1},
		{"quotient-overflow", math.MaxInt64, time.Second, time.Second / 2, math.MaxInt64},
		{"division-overflow", math.MaxInt64, time.Duration(math.MaxInt64), time.Nanosecond, math.MaxInt64},
		{"zero-window", 1000, time.Second, 0, 0},
		{"future-start", 1000, time.Second, -time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(100, 0)
			s := Speeds{count: tc.count, interval: tc.interval, nowTime: start}
			now := start.Add(tc.elapsed)
			if got := s.sampleLocked(now); got != tc.want {
				t.Fatalf("got=%d want=%d", got, tc.want)
			}
			if tc.elapsed >= tc.interval {
				if s.count != 0 || !s.nowTime.Equal(now) {
					t.Fatal("completed sample not reset")
				}
			} else if s.count != tc.count || !s.nowTime.Equal(start) {
				t.Fatal("incomplete sample changed")
			}
		})
	}
}

func TestSpeedNonPositiveCountsPreserveSample(t *testing.T) {
	var s Speeds
	s.Add(25)
	s.Add(-1)
	s.Add(math.MinInt64)
	s.Add(0)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count != 25 {
		t.Fatal(s.count)
	}
}
