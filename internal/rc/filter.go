package rc

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"
)

type Filter struct {
	Include, Exclude []*regexp.Regexp
	Files            map[string]bool
}

func glob(s string) (*regexp.Regexp, error) {
	if s == "" {
		return nil, fmt.Errorf("empty filter")
	}
	anchored := strings.HasPrefix(s, "/")
	s = strings.TrimPrefix(s, "/")
	var b strings.Builder
	if anchored || strings.Contains(s, "/") {
		b.WriteString("^")
	} else {
		b.WriteString("(^|/)")
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*':
			if i+1 < len(s) && s[i+1] == '*' {
				i++
				if i+1 < len(s) && s[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '[':
			j := strings.IndexByte(s[i+1:], ']')
			if j < 0 {
				return nil, fmt.Errorf("invalid filter %q", s)
			}
			part := s[i : i+j+2]
			if strings.HasPrefix(part, "[!") {
				part = "[^" + part[2:]
			}
			b.WriteString(part)
			i += j + 1
		default:
			b.WriteString(regexp.QuoteMeta(s[i : i+1]))
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
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
func (f Filter) Match(p string) bool {
	if f.Files != nil && !f.Files[p] {
		return false
	}
	if f.excluded(p) {
		return false
	}
	if len(f.Include) == 0 {
		return true
	}
	for _, r := range f.Include {
		if r.MatchString(p) {
			return true
		}
	}
	return false
}

func (f Filter) excluded(p string) bool {
	for current := p; current != "." && current != "/"; current = path.Dir(current) {
		for _, r := range f.Exclude {
			if r.MatchString(current) || r.MatchString(current+"/") {
				return true
			}
		}
	}
	return false
}
