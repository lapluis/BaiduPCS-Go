package rc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesFromUTF8BOM(t *testing.T) {
	for _, prefix := range []string{"", "\ufeff", "\ufeff# selected files\r\n", "\ufeff\r\n"} {
		t.Run(prefix, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, s, "first.txt", "first")
			write(t, s, "second.txt", "second")
			write(t, s, "other.txt", "other")
			list := filepath.Join(t.TempDir(), "files.txt")
			if err := os.WriteFile(list, []byte(prefix+"first.txt\r\nsecond.txt\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			o := opts()
			var err error
			o.Filter, err = NewFilter(nil, nil, list)
			if err != nil {
				t.Fatal(err)
			}
			p, err := BuildPlan(context.Background(), s, d, "copy", o)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Execute(context.Background(), p, o)
			if err != nil || result.Copied != 2 {
				t.Fatal(result, err)
			}
			for _, name := range []string{"first.txt", "second.txt"} {
				if _, err = os.Stat(d.At(name)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = os.Stat(d.At("other.txt")); !os.IsNotExist(err) {
				t.Fatal("unlisted file copied", err)
			}
		})
	}
}

func TestFilesFromPreservesLaterBOM(t *testing.T) {
	list := filepath.Join(t.TempDir(), "files.txt")
	if err := os.WriteFile(list, []byte("first.txt\n\ufeffliteral.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := NewFilter(nil, nil, list)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Match("\ufeffliteral.txt") || f.Match("literal.txt") {
		t.Fatal("BOM stripped outside start of file")
	}
}
