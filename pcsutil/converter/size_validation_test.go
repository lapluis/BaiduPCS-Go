package converter_test

import (
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
)

func TestParseFileSizeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  int64
	}{
		{"0", 0}, {".5KB", 512}, {"1.5mB", 1572864}, {"1.", 1},
		{"1.9B", 1}, {"00012", 12}, {"0.0009765625KB", 1},
		{"9007199254740993", 9007199254740993},
		{"9223372036854775807", 9223372036854775807},
		{"8191PB", 9222246136947933184},
	} {
		got, err := converter.ParseFileSizeStr(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("%q: got %d, %v; want %d", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"", ".", "..", "1.2.3MB", "1..KB", "-1", "NaN", "Inf", "1e3", "1XB", "9223372036854775808", "8192PB", strings.Repeat("9", 400) + "GB"} {
		if got, err := converter.ParseFileSizeStr(input); err == nil {
			t.Errorf("invalid input %q accepted as %d", input, got)
		}
	}
}
