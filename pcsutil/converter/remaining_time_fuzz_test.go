package converter

import (
	"math"
	"math/big"
	"testing"
	"time"
)

func FuzzRemainingTime(f *testing.F) {
	for _, seed := range [][3]int64{
		{0, 0, 1},
		{10, 7, 2},
		{10, 20, 1},
		{1, 0, math.MaxInt64},
		{math.MaxInt64, 0, 1},
		{math.MaxInt64, 0, int64(time.Second)},
		{math.MaxInt64, 0, int64(time.Second) - 1},
		{math.MaxInt64 - 1, 0, math.MaxInt64},
		{math.MaxInt64, math.MaxInt64 - 1, 1},
		{92233720369, 0, 10},
		{-1, 0, 1},
		{math.MinInt64, math.MinInt64, math.MinInt64},
		{math.MaxInt64, -1, 1},
		{10, 7, 0},
		{10, 7, -1},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, total, done, rate int64) {
		want := time.Duration(-1)
		if total >= 0 && done >= 0 && rate > 0 {
			if done >= total {
				want = 0
			} else {
				// An independent exact-arithmetic oracle: compute the complete
				// nanosecond numerator before dividing, with no int64 overflow.
				numerator := new(big.Int).Sub(big.NewInt(total), big.NewInt(done))
				numerator.Mul(numerator, big.NewInt(int64(time.Second)))
				nanoseconds := numerator.Quo(numerator, big.NewInt(rate))
				if nanoseconds.IsInt64() {
					want = time.Duration(nanoseconds.Int64())
				}
			}
		}
		if got := RemainingTime(total, done, rate); got != want {
			t.Fatalf("RemainingTime(%d,%d,%d)=%v, want %v", total, done, rate, got, want)
		}
	})
}
