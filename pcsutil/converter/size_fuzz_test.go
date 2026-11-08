package converter_test

import (
	"math/big"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
)

// Build decimal sizes from integer coefficients. The oracle never reparses the
// generated text or uses the production parser's rational-number conversion.
func FuzzParseFileSizeDecimalModel(f *testing.F) {
	units := []struct {
		text  string
		shift uint
	}{{"", 0}, {"B", 0}, {"k", 10}, {"KB", 10}, {"mB", 20}, {"G", 30}, {"gb", 30}, {"T", 40}, {"Tb", 40}, {"pB", 50}}
	for _, seed := range []struct {
		whole, fraction uint64
		places, padding uint8
	}{
		{0, 0, 0, 0}, {0, 1, 1, 64}, {1, 9, 1, 7}, {12, 0, 0, 128},
		{1<<53 + 1, 0, 0, 0}, {1<<63 - 1, 0, 0, 0}, {1<<63 - 1, 1, 20, 0},
		{1 << 63, 0, 0, 0}, {^uint64(0), ^uint64(0), 20, 7}, {8191, 0, 0, 0}, {8192, 0, 0, 0},
	} {
		for i := range units {
			f.Add(seed.whole, seed.fraction, seed.places, seed.padding, uint8(i))
		}
	}
	f.Fuzz(func(t *testing.T, whole, fraction uint64, places, padding, unitIndex uint8) {
		decimals := int(places % 21)
		unit := units[int(unitIndex)%len(units)]
		scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
		part := new(big.Int).Mod(new(big.Int).SetUint64(fraction), scale)
		coefficient := new(big.Int).Mul(new(big.Int).SetUint64(whole), scale)
		coefficient.Add(coefficient, part)
		numerator := new(big.Int).Lsh(coefficient, unit.shift)
		limit := new(big.Int).Mul(big.NewInt(1<<63-1), scale)
		inRange := numerator.Cmp(limit) <= 0
		want := new(big.Int).Quo(numerator, scale)

		numeric := strings.Repeat("0", int(padding%8)) + strconv.FormatUint(whole, 10)
		if decimals > 0 {
			if whole == 0 && padding&64 != 0 {
				numeric = ""
			}
			fractionText := part.String()
			numeric += "." + strings.Repeat("0", decimals-len(fractionText)) + fractionText
		} else if padding&128 != 0 {
			numeric += "."
		}
		input := numeric + unit.text
		got, err := converter.ParseFileSizeStr(input)
		if inRange {
			if err != nil || got != want.Int64() {
				t.Fatalf("%q: got=%d err=%v want=%s", input, got, err, want)
			}
		} else if err == nil || got != 0 {
			t.Fatalf("out-of-range %q returned %d, %v", input, got, err)
		}
		// These mutations are outside the accepted unsigned decimal grammar,
		// regardless of whether the original amount fits the result type.
		for _, invalid := range []string{"+" + input, "-" + input, input + "!", "." + numeric + "." + unit.text, numeric + "XB"} {
			if got, err := converter.ParseFileSizeStr(invalid); err == nil || got != 0 {
				t.Fatalf("invalid size %q returned %d, %v", invalid, got, err)
			}
		}
	})
}
