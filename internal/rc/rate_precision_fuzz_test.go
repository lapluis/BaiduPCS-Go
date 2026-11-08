package rc

import (
	"math/big"
	"strconv"
	"strings"
	"testing"
)

func FuzzBandwidthRateExactRatio(f *testing.F) {
	for _, whole := range []uint64{0, 1, 2, 1<<53 - 1, 1<<53 + 1, 1<<62 - 1, 1 << 62, 1<<62 + 1, 1<<64 - 1} {
		for _, fraction := range []uint64{0, 1, 1<<64 - 1} {
			for mode := uint8(0); mode < 4; mode++ {
				f.Add(whole, fraction, uint8(19), mode, uint8(mode%4))
			}
		}
	}
	f.Fuzz(func(t *testing.T, whole, fraction uint64, places, mode, unit uint8) {
		digits := int(places % 21)
		base := int64(10)
		if mode&1 != 0 {
			base = 16
		}
		denominator := new(big.Int).Exp(big.NewInt(base), big.NewInt(int64(digits)), nil)
		part := new(big.Int).Mod(new(big.Int).SetUint64(fraction), denominator)
		numerator := new(big.Int).Mul(new(big.Int).SetUint64(whole), denominator)
		numerator.Add(numerator, part)
		padding := strings.Repeat("0", int(mode>>4)%8)
		var numeric string
		if base == 16 {
			numeric = "0x" + padding + numerator.Text(16) + "p-" + strconv.Itoa(4*digits)
		} else if mode&2 != 0 {
			numeric = padding + numerator.String() + "e-" + strconv.Itoa(digits)
		} else {
			numeric = padding + strconv.FormatUint(whole, 10)
			if digits > 0 {
				text := part.String()
				numeric += "." + strings.Repeat("0", digits-len(text)) + text
			}
		}
		suffix := []string{"", "K", "M", "G"}[unit%4]
		if mode&4 != 0 {
			suffix = strings.ToLower(suffix)
		}
		input := numeric + suffix
		if mode&8 != 0 {
			input = "+" + input
		}
		// The oracle uses scaled integers and a power denominator; production
		// uses a grammar check followed by big.Rat parsing and multiplication.
		scaled := new(big.Int).Lsh(new(big.Int).Set(numerator), uint(10*(unit%4)))
		ceiling := new(big.Int).Lsh(new(big.Int).Set(denominator), 62)
		invalid := scaled.Sign() != 0 && (scaled.Cmp(denominator) < 0 || scaled.Cmp(ceiling) > 0)
		want := int64(0)
		if !invalid {
			want = new(big.Int).Quo(scaled, denominator).Int64()
		}
		got, err := ParseBandwidthRate(input)
		if got != want || (err != nil) != invalid {
			t.Fatalf("%q: got=%d err=%v want=%d invalid=%t", input, got, err, want, invalid)
		}
		negative, err := ParseBandwidthRate("-" + numeric + suffix)
		if numerator.Sign() == 0 {
			if err != nil || negative != 0 {
				t.Fatalf("negative zero changed: %q -> %d, %v", numeric, negative, err)
			}
		} else if err == nil || negative != 0 {
			t.Fatalf("negative rate accepted: %q -> %d, %v", numeric, negative, err)
		}
	})
}
