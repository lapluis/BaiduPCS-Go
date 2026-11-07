package rc

import (
	"strings"
	"testing"
)

func TestBandwidthParsingDoesNotTurnPositiveRatesIntoUnlimited(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    int64
		invalid bool
	}{
		{"0", 0, false},
		{"0K", 0, false},
		{"0.0M", 0, false},
		{"0e100", 0, false},
		{"0x0p-2000", 0, false},
		{"1", 1, false},
		{"1.5", 1, false},
		{"0.5K", 512, false},
		{"0.0009765625K", 1, false},
		{"1.5M", 1572864, false},
		{"4G", 4294967296, false},
		{"4611686018427387904", 1 << 62, false},
		{"0.5", 0, true},
		{"1e-400", 0, true},
		{"1e-400G", 0, true},
		{"-1e-400", 0, true},
		{"0x1p-2000", 0, true},
		{"0.0001K", 0, true},
		{"0.0000005M", 0, true},
		{"0.0000000005G", 0, true},
		{"0.0009765624999999999K", 0, true},
		{"-1", 0, true},
		{"NaN", 0, true},
		{"+Inf", 0, true},
		{"1e100", 0, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := parseRate(tc.input)
			if (err != nil) != tc.invalid || got != tc.want {
				t.Errorf("parseRate(%q)=%d err=%v, want %d invalid=%t", tc.input, got, err, tc.want, tc.invalid)
			}
			p, err := parse([]string{"--bwlimit", tc.input})
			if (err != nil) != tc.invalid || err == nil && p.BWLimit != tc.want {
				t.Errorf("CLI parsed %q as %d err=%v", tc.input, p.BWLimit, err)
			}
			if err != nil && !strings.Contains(err.Error(), "--bwlimit") {
				t.Errorf("flag diagnostic lost: %v", err)
			}
		})
	}
}
