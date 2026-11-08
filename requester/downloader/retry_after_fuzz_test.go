package downloader

import (
	"math"
	"math/big"
	"strings"
	"testing"
	"time"
)

// Compare decimal delay headers with arbitrary-precision nanoseconds, rather
// than the parser's bounded integer conversion and seconds threshold.
func FuzzRetryAfterDecimalDelay(f *testing.F) {
	for _, digits := range []string{
		"", "0", "0000", "1", "0001", "60", "2147483647", "4294967296",
		"9223372035", "9223372036", "9223372037", "9223372036854775807",
		"18446744073709551615", "18446744073709551616",
		strings.Repeat("9", 128), strings.Repeat("0", 128) + "1",
	} {
		for mode := uint8(0); mode < 4; mode++ {
			f.Add([]byte(digits), mode, uint32(999999999))
		}
	}
	f.Fuzz(func(t *testing.T, raw []byte, padding uint8, nanos uint32) {
		if len(raw) > 2048 {
			t.Skip()
		}
		// Every fuzz input exercises the numeric branch; preserve seed digits
		// and map other bytes to digits without using the production parser.
		digits := make([]byte, len(raw))
		for i, b := range raw {
			if b >= '0' && b <= '9' {
				digits[i] = b
			} else {
				digits[i] = '0' + b%10
			}
		}
		numeric := string(digits)
		spaces := []string{"", " \t", "\r\n", "\u2003"}[padding%4]
		now := time.Date(2026, 11, 9, 12, 0, 0, int(nanos%1000000000), time.UTC)
		seconds := new(big.Int)
		if numeric != "" {
			if _, ok := seconds.SetString(numeric, 10); !ok {
				t.Fatal("invalid generated decimal")
			}
		}
		want := time.Time{}
		if seconds.Sign() != 0 {
			delay := new(big.Int).Mul(seconds, big.NewInt(1000000000))
			ceiling := big.NewInt(math.MaxInt64)
			if delay.Cmp(ceiling) > 0 {
				delay.Set(ceiling)
			}
			want = now.Add(time.Duration(delay.Int64()))
		}
		got := retryAfterDeadline(spaces+numeric+spaces, now)
		if !got.Equal(want) {
			t.Fatalf("delay %q: deadline=%s want=%s", numeric, got, want)
		}
		if numeric != "" {
			if prefixed := retryAfterDeadline("000"+numeric, now); !prefixed.Equal(want) {
				t.Fatalf("leading zeros changed delay %q: %s want=%s", numeric, prefixed, want)
			}
		}
		for _, invalid := range []string{"+" + numeric, "-" + numeric, numeric + "x", numeric + ".5", numeric + "_0"} {
			if got := retryAfterDeadline(invalid, now); !got.IsZero() {
				t.Fatalf("invalid delay %q produced %s", invalid, got)
			}
		}
	})
}
