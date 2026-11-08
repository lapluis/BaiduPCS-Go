package rc

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode"
)

type Filter struct {
	Include, Exclude []*regexp.Regexp
	Files            map[string]bool
}

// A glob character class consumes one filename rune, never a separator.
// Subtract slash from parsed ranges, including ranges that span it (.-0).
func componentClass(part string) (string, error) {
	parsed, err := syntax.Parse(part, syntax.Perl)
	if err != nil {
		return "", err
	}
	var source []rune
	switch parsed.Op {
	case syntax.OpLiteral:
		// The parser may simplify [Aa] to a case-folded literal. Retain
		// that flag when the single character cannot match a separator.
		if len(parsed.Rune) == 1 && parsed.Rune[0] != '/' {
			return parsed.String(), nil
		}
		for _, r := range parsed.Rune {
			source = append(source, r, r)
		}
	case syntax.OpCharClass:
		source = parsed.Rune
	case syntax.OpAnyChar:
		source = []rune{0, unicode.MaxRune}
	case syntax.OpAnyCharNotNL:
		source = []rune{0, '\n' - 1, '\n' + 1, unicode.MaxRune}
	default:
		return "", fmt.Errorf("invalid filter class %q", part)
	}
	var runes []rune
	for i := 0; i < len(source); i += 2 {
		lo, hi := source[i], source[i+1]
		if lo > '/' || hi < '/' {
			runes = append(runes, lo, hi)
			continue
		}
		if lo < '/' {
			runes = append(runes, lo, '/'-1)
		}
		if hi > '/' {
			runes = append(runes, '/'+1, hi)
		}
	}
	return (&syntax.Regexp{Op: syntax.OpCharClass, Rune: runes}).String(), nil
}

func glob(s string) (*regexp.Regexp, error) {
	if s == "" {
		return nil, fmt.Errorf("empty filter")
	}
	anchored := strings.HasPrefix(s, "/")
	s = strings.TrimPrefix(s, "/")
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*':
			if i+1 < len(s) && s[i+1] == '*' {
				// Filenames may contain newlines on Unix and remote backends.
				// Recursive wildcards must cover them as well as separators.
				i++
				if i+1 < len(s) && s[i+1] == '/' {
					anchored = true
					i++
					b.WriteString("(?s:.*/)?")
				} else {
					b.WriteString("(?s:.*)")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' {
					// An escaped bracket belongs to the class, not its delimiter.
					j += 2
					continue
				}
				if strings.HasPrefix(s[j:], "[:") {
					// POSIX classes have their own closing bracket.
					end := strings.Index(s[j+2:], ":]")
					if end < 0 {
						return nil, fmt.Errorf("invalid filter %q", s)
					}
					j += end + 4
					continue
				}
				if s[j] == ']' {
					break
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("invalid filter %q", s)
			}
			part := s[i : j+1]
			if strings.HasPrefix(part, "[!") {
				part = "[^" + part[2:]
			}
			class, err := componentClass(part)
			if err != nil {
				return nil, err
			}
			b.WriteString(class)
			i = j
		default:
			// Separators inside a character class do not make a basename
			// pattern root-relative; the entire class was consumed above.
			if s[i] == '/' {
				anchored = true
			}
			b.WriteString(regexp.QuoteMeta(s[i : i+1]))
		}
	}
	b.WriteString("$")
	prefix := "(^|/)"
	if anchored {
		prefix = "^"
	}
	return regexp.Compile(prefix + b.String())
}
func NewFilter(include, exclude []string, files string) (Filter, error) {
	f := Filter{}
	for _, group := range []struct {
		in  []string
		out *[]*regexp.Regexp
	}{{include, &f.Include}, {exclude, &f.Exclude}} {
		for _, s := range group.in {
			r, err := glob(s)
			if err != nil {
				return f, err
			}
			*group.out = append(*group.out, r)
		}
	}
	if files != "" {
		h, err := os.Open(files)
		if err != nil {
			return f, err
		}
		defer h.Close()
		f.Files = map[string]bool{}
		scan := bufio.NewScanner(h)
		scan.Buffer(make([]byte, 4096), 1024*1024)
		firstLine := true
		for scan.Scan() {
			line := scan.Text()
			if firstLine {
				line = strings.TrimPrefix(line, "\uFEFF")
				firstLine = false
			}
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			if strings.Contains(s, `\`) || strings.HasPrefix(s, "/") || s == ".." || strings.HasPrefix(path.Clean(s), "../") {
				return f, fmt.Errorf("files-from requires relative paths: %q", s)
			}
			f.Files[path.Clean(s)] = true
		}
		if err = scan.Err(); err != nil {
			return f, err
		}
	}
	return f, nil
}

// Match selects an ordinary file using its root-relative path.
func (f Filter) Match(p string) bool {
	return f.match(p, false)
}

// MatchDirectory selects a directory, including patterns ending in slash.
func (f Filter) MatchDirectory(p string) bool {
	return f.match(p, true)
}

func (f Filter) match(p string, directory bool) bool {
	if f.Files != nil && !f.Files[p] {
		return false
	}
	if f.excludedPath(p, directory) {
		return false
	}
	if len(f.Include) == 0 {
		return true
	}
	for _, r := range f.Include {
		if r.MatchString(p) || (directory && r.MatchString(p+"/")) {
			return true
		}
	}
	return false
}

func (f Filter) excluded(p string) bool {
	return f.excludedPath(p, true)
}

func (f Filter) excludedPath(p string, directory bool) bool {
	for current := p; current != "." && current != "/"; current = path.Dir(current) {
		for _, r := range f.Exclude {
			if r.MatchString(current) || ((directory || current != p) && r.MatchString(current+"/")) {
				return true
			}
		}
	}
	return false
}
