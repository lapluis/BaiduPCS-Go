package rc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWindowsAndRemotes(t *testing.T) {
	for _, s := range []string{`D:\Backup`, `D:/Backup`, `\\server\share`, `./a:b`} {
		if IsRemote(s) {
			t.Fatalf("local path classified remote: %s", s)
		}
	}
	if !IsRemote("bd:/Backup") {
		t.Fatal("remote not recognized")
	}
	r := &Resolver{CWD: t.TempDir(), Aliases: map[string]uint64{}}
	e, err := r.Resolve("relative")
	if err != nil || e.Root != filepath.Join(r.CWD, "relative") {
		t.Fatal(e, err)
	}
	if _, err = r.Resolve("missing:/a"); err == nil {
		t.Fatal("unknown remote accepted")
	}
}
func TestFilter(t *testing.T) {
	f, err := NewFilter([]string{"**/*.txt"}, []string{"secret/**"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{"a.txt": true, "a/b.txt": true, "secret/a.txt": false, "a.jpg": false} {
		if f.Match(p) != want {
			t.Fatal(p)
		}
	}
}
func TestLocalHashAndEmptyRemoval(t *testing.T) {
	ctx := context.Background()
	l := &Local{}
	d := t.TempDir()
	p := filepath.Join(d, "a")
	os.WriteFile(p, []byte("abc"), 0600)
	h, err := l.Hash(ctx, p)
	if err != nil || h != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(h, err)
	}
	if err = l.Remove(ctx, d, true); err == nil {
		t.Fatal("nonempty directory removed")
	}
}
