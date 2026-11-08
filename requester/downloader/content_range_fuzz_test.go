package downloader

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func FuzzContentRangeNumericBounds(f *testing.F) {
	for _, seed := range [][3]uint64{
		{0, 0, 1}, {4, 7, 8}, {8, 7, 16}, {0, 8, 8},
		{0, 0, 0}, {math.MaxInt64 - 1, math.MaxInt64 - 1, math.MaxInt64},
		{0, math.MaxInt64, math.MaxUint64},
		{math.MaxInt64 + 1, math.MaxInt64 + 1, math.MaxUint64},
		{math.MaxUint64, math.MaxUint64, math.MaxUint64},
	} {
		for _, unknown := range []bool{false, true} {
			for mutation := uint8(0); mutation < 8; mutation++ {
				f.Add(seed[0], seed[1], seed[2], unknown, mutation, uint8(31))
			}
		}
	}
	f.Fuzz(func(t *testing.T, begin, end, total uint64, unknown bool, mutation, letterCase uint8) {
		unit := []byte("bytes")
		for i := range unit {
			if letterCase&(1<<i) != 0 {
				unit[i] -= 'a' - 'A'
			}
		}
		totalText := fmt.Sprint(total)
		if unknown {
			totalText = "*"
		}
		value := fmt.Sprintf("%s %d-%d/%s", unit, begin, end, totalText)
		// The model uses unsigned values, including numbers unrepresentable
		// by the parser's signed output. It never parses the generated text.
		valid := begin <= end && end <= math.MaxInt64 && (unknown || total > end && total <= math.MaxInt64)
		switch mutation % 8 {
		case 1:
			value = "byteſ" + value[5:]
		case 2:
			value = value[:6] + "-" + value[6:]
		case 3:
			value = value[:6] + "+" + value[6:]
		case 4:
			value += "x"
		case 5:
			value = strings.Replace(value, "/", ":", 1)
		case 6:
			value = value[:6] + " " + value[6:]
		case 7:
			value += "\x00"
		}
		valid = valid && mutation%8 == 0
		gotBegin, gotEnd, gotTotal, ok := parseContentRangeBounds(value)
		if ok != valid {
			t.Fatalf("range %q accepted=%t want=%t", value, ok, valid)
		}
		wantTotal := int64(-1)
		if valid {
			if !unknown {
				wantTotal = int64(total)
			}
			if gotBegin != int64(begin) || gotEnd != int64(end) || gotTotal != wantTotal {
				t.Fatalf("range %q decoded as %d-%d/%d", value, gotBegin, gotEnd, gotTotal)
			}
		}
		if got := ParseContentRange(value); got != wantTotal {
			t.Fatalf("public parser %q total=%d want=%d", value, got, wantTotal)
		}
	})
}
