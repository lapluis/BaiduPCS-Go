package rc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMoveToWindowsAliasRetainsOnlyCopy(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows aliases")
	}
	for _, suffix := range []string{"."} {
		t.Run(suffix, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "file.txt")
			if err := os.WriteFile(file, []byte("only copy"), 0600); err != nil {
				t.Fatal(err)
			}
			resolver := &Resolver{CWD: root}
			src, err := resolver.Resolve(file)
			if err != nil {
				t.Fatal(err)
			}
			dst, err := resolver.Resolve(filepath.Join(root+suffix, "file.txt"))
			if err != nil {
				t.Fatal(err)
			}
			a, err := os.Stat(src.Root)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.Stat(dst.Root)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(a, b) {
				t.Fatal("fixture does not alias source")
			}
			for _, move := range []bool{false, true} {
				if _, err = FilePlan(context.Background(), src, dst, move, opts()); err == nil || !strings.Contains(err.Error(), "overlap") {
					t.Fatal("alias accepted", err)
				}
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "only copy" {
				t.Fatal(string(data), err)
			}
		})
	}
}

func TestDirectoryOverlapThroughWindowsAlias(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows aliases")
	}
	root := t.TempDir()
	a := Endpoint{FS: &Local{}, Root: root}
	b := Endpoint{FS: &Local{}, Root: filepath.Join(root+".", "new", "nested")}
	for _, pair := range [][2]Endpoint{{a, b}, {b, a}} {
		if err := validatePair(pair[0], pair[1]); err == nil {
			t.Fatal("aliased ancestor accepted")
		}
	}
	if _, err := os.Stat(filepath.Join(root, "new")); !os.IsNotExist(err) {
		t.Fatal("validation mutated source", err)
	}
}

func TestMoveHardLinkProtection(t *testing.T) {
	for _, afterPlanning := range []bool{false, true} {
		s, d := endpoints(t)
		write(t, s, "file", "content")
		write(t, d, "file", "content")
		src := Endpoint{FS: s.FS, Root: s.At("file")}
		dst := Endpoint{FS: d.FS, Root: d.At("file")}
		var plan *Plan
		var err error
		if afterPlanning {
			plan, err = FilePlan(context.Background(), src, dst, true, opts())
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = os.Remove(dst.Root); err != nil {
			t.Fatal(err)
		}
		if err = os.Link(src.Root, dst.Root); err != nil {
			t.Skip("hard links unavailable:", err)
		}
		if afterPlanning {
			_, err = Execute(context.Background(), plan, opts())
			if err == nil || !strings.Contains(err.Error(), "same file") {
				t.Fatal("late alias not rejected before removal", err)
			}
		} else {
			if _, err = FilePlan(context.Background(), src, dst, true, opts()); err == nil {
				t.Fatal("hard link accepted during planning")
			}
		}
		for _, file := range []string{src.Root, dst.Root} {
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "content" {
				t.Fatal(file, string(data), err)
			}
		}
	}
}

func TestMoveEmptyCleanupRespectsExclusions(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "selected/file.txt", "content")
	for _, dir := range []string{"excluded/nested", "parent/protected", "ordinary/nested"} {
		if err := os.MkdirAll(s.At(dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	o := opts()
	o.DeleteEmptyDirs = true
	o.Filter, _ = NewFilter(nil, []string{"excluded/", "parent/protected"}, "")
	plan, err := BuildPlan(context.Background(), s, d, "move", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Execute(context.Background(), plan, o); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"excluded/nested", "parent/protected", "parent"} {
		if _, err = os.Stat(s.At(dir)); err != nil {
			t.Fatal("excluded directory or ancestor removed", dir, err)
		}
	}
	for _, dir := range []string{"selected", "ordinary"} {
		if _, err = os.Stat(s.At(dir)); !os.IsNotExist(err) {
			t.Fatal("eligible empty directory retained", dir, err)
		}
	}
	data, err := os.ReadFile(d.At("selected/file.txt"))
	if err != nil || string(data) != "content" {
		t.Fatal(string(data), err)
	}
}
