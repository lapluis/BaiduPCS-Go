package rc

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/mattn/go-runewidth"
)

func TestProgressLinesDoNotRenderControlFilenames(t *testing.T) {
	for control := rune(0); control <= 159; control++ {
		if control > 31 && control < 127 {
			continue
		}
		t.Run(fmt.Sprintf("%02x", control), func(t *testing.T) {
			path := "名前/" + string(control) + "[2J-file+%20.mkv"
			p := ProgressSnapshot{Done: 1, Total: 2, Active: 1, Count: 1, Files: []FileProgress{{Path: path, Done: 1, Total: 2}}}
			lines := p.Lines()
			for _, line := range lines {
				if strings.ContainsFunc(line, unicode.IsControl) || runewidth.StringWidth(line) > 78 {
					t.Fatalf("unsafe or wrapping progress line: %q", line)
				}
			}
			if len(lines) != 3 || !strings.Contains(lines[2], "名前/") || !strings.Contains(lines[2], "file+%20.mkv") || p.Files[0].Path != path {
				t.Fatalf("rendering changed file identity or ordinary name: lines=%q path=%q", lines, p.Files[0].Path)
			}
		})
	}
}
