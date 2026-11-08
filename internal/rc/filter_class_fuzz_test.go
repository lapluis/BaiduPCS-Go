package rc

import (
	"fmt"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzFilterClassRanges(f *testing.F) {
	for _, seed := range [][3]uint32{
		{'.', '0', '/'}, {'a', 'z', 'm'}, {0, unicode.MaxRune, '\n'},
		{'/', '/', '/'}, {'/', 'Y', 'Y'}, {'α', 'ω', 'λ'}, {'A', 'Z', 'a'},
	} {
		f.Add(seed[0], seed[1], seed[2], false)
		f.Add(seed[0], seed[1], seed[2], true)
	}
	f.Fuzz(func(t *testing.T, low, high, value uint32, negate bool) {
		lo, hi, r := rune(low%(unicode.MaxRune+1)), rune(high%(unicode.MaxRune+1)), rune(value%(unicode.MaxRune+1))
		if !utf8.ValidRune(lo) || !utf8.ValidRune(hi) || !utf8.ValidRune(r) {
			return
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		prefix := ""
		if negate {
			prefix = "!"
		}
		pattern := fmt.Sprintf(`a[%s\x{%X}-\x{%X}]b`, prefix, lo, hi)
		name := "a" + string(r) + "b"
		want := lo <= r && r <= hi
		if negate {
			want = !want
		}
		want = want && r != '/'
		// Literal and hex-escaped slashes describe the same character set.
		// Neither turns this basename rule into a root-relative path rule.
		for _, rule := range []string{pattern, strings.ReplaceAll(pattern, `\x{2F}`, "/")} {
			included, err := NewFilter([]string{rule}, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			excluded, err := NewFilter(nil, []string{rule}, "")
			if err != nil {
				t.Fatal(err)
			}
			for _, parent := range []string{"", "dir/", "dir/deep/"} {
				candidate := parent + name
				if included.Match(candidate) != want || excluded.Match(candidate) == want {
					t.Fatalf("rule %q name=%q include=%t exclude=%t want=%t/%t", rule, candidate, included.Match(candidate), excluded.Match(candidate), want, !want)
				}
			}
		}
	})
}
