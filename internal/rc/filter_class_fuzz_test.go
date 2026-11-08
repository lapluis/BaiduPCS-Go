package rc

import (
	"fmt"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzFilterClassRanges(f *testing.F) {
	for _, seed := range [][3]uint32{
		{'.', '0', '/'}, {'a', 'z', 'm'}, {0, unicode.MaxRune, '\n'},
		{'/', '/', '/'}, {'α', 'ω', 'λ'}, {'A', 'Z', 'a'},
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
		included, err := NewFilter([]string{pattern}, nil, "")
		if err != nil || included.Match(name) != want {
			t.Fatalf("include %q matched %q = %t, want %t, err=%v", pattern, name, included.Match(name), want, err)
		}
		excluded, err := NewFilter(nil, []string{pattern}, "")
		if err != nil || excluded.Match(name) == want {
			t.Fatalf("exclude %q matched %q = %t, want %t, err=%v", pattern, name, excluded.Match(name), !want, err)
		}
	})
}
