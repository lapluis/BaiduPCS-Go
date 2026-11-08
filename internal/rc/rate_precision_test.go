package rc

import "testing"

func TestBandwidthParsingChecksExactNumericBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    int64
		invalid bool
	}{
		{"0.999999999999999999999999", 0, true},
		{"0.000976562499999999999999999K", 0, true},
		{"1.9999999999999999999", 1, false},
		{"9007199254740993", 9007199254740993, false},
		{"9_007_199_254_740_993", 9007199254740993, false},
		{"4611686018427387903", 1<<62 - 1, false},
		{"4611686018427387904", 1 << 62, false},
		{"4611686018427387904.0001", 0, true},
		{"4611686018427387905", 0, true},
		{"4503599627370495.999999999999K", 1<<62 - 1, false},
		{"4294967296.000000000000000001G", 0, true},
		{"0x1.ffffffffffffffffffp0", 1, false},
		{"0x1.0000000000000001p62", 0, true},
		{"0x1p0", 1, false}, {"1_024", 1024, false},
		{"1e-999999999", 0, true}, {"0x1p-999999999", 0, true},
		{"0e99999999999999", 0, false}, {"0x0p99999999999999", 0, false},
		{"-0", 0, false}, {"-0x0p2000", 0, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseBandwidthRate(tc.input)
			if got != tc.want || (err != nil) != tc.invalid {
				t.Fatalf("rate %q: got=%d err=%v want=%d invalid=%t", tc.input, got, err, tc.want, tc.invalid)
			}
			options, err := parse([]string{"--bwlimit=" + tc.input})
			if (err != nil) != tc.invalid || err == nil && options.BWLimit != tc.want {
				t.Errorf("legacy command rate changed: got=%d err=%v", options.BWLimit, err)
			}
		})
	}
}
