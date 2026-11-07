package converter

import (
	"math"
	"testing"
	"time"
)

func TestRemainingTime(t *testing.T) {
	for _, tc := range []struct {
		name              string
		total, done, rate int64
		want              time.Duration
	}{
		{"fraction", 10, 7, 2, 1500 * time.Millisecond},
		{"max-duration", math.MaxInt64, 0, int64(time.Second), time.Duration(math.MaxInt64)},
		{"above-max-duration", math.MaxInt64, 0, int64(time.Second) - 1, -1},
		{"overflow-fraction", 92233720369, 0, 10, -1},
		{"near-max-fraction", 92233720368, 0, 10, 9223372036800000000},
		{"large-fraction", math.MaxInt64 - 1, 0, math.MaxInt64, time.Second - 1},
		{"max-rate", math.MaxInt64, 0, math.MaxInt64, time.Second},
		{"overflow-whole-seconds", 128 << 30, 0, 1, -1},
		{"unknown-size", -1, 10, 1, -1},
		{"negative-done", math.MaxInt64, -1, 1, -1},
		{"no-rate", 10, 2, 0, -1},
		{"negative-rate", 10, 2, -1, -1},
		{"empty", 0, 0, 1, 0},
		{"over-complete", 10, math.MaxInt64, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := RemainingTime(tc.total, tc.done, tc.rate); got != tc.want {
				t.Fatalf("RemainingTime(%d,%d,%d)=%v, want %v", tc.total, tc.done, tc.rate, got, tc.want)
			}
		})
	}
}
