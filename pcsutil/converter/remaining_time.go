package converter

import (
	"math"
	"math/bits"
	"time"
)

// RemainingTime estimates transfer duration. A negative result means unknown,
// including estimates outside the range of time.Duration.
func RemainingTime(total, done, rate int64) time.Duration {
	if total < 0 || done < 0 || rate <= 0 {
		return -1
	}
	if done >= total {
		return 0
	}
	remaining := total - done
	seconds := remaining / rate
	if seconds > math.MaxInt64/int64(time.Second) {
		return -1
	}
	whole := seconds * int64(time.Second)
	// Multiply the remainder without overflowing or rounding at duration limits.
	hi, lo := bits.Mul64(uint64(remaining%rate), uint64(time.Second))
	fraction, _ := bits.Div64(hi, lo, uint64(rate))
	if fraction > uint64(math.MaxInt64-whole) {
		return -1
	}
	return time.Duration(whole + int64(fraction))
}
