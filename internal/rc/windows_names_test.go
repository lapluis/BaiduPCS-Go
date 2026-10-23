package rc

import (
	"context"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"
)

// Synthetic remote inventory permits names Windows cannot faithfully create.
type windowsNameSource struct {
	Local
	entries map[string]Entry
}

func (*windowsNameSource) Identity() string { return "windows-name-fixture" }
func (s *windowsNameSource) Stat(_ context.Context, p string) (Entry, error) {
	if p == "/source" {
		return Entry{IsDir: true}, nil
	}
	if e, ok := s.entries[strings.TrimPrefix(p, "/source/")]; ok {
		return e, nil
	}
	return Entry{}, os.ErrNotExist
}
func (s *windowsNameSource) List(_ context.Context, p string) ([]Entry, error) {
	var out []Entry
	for name, e := range s.entries {
		if path.Dir("/source/"+name) == p {
			e.Name = path.Base(name)
			out = append(out, e)
		}
	}
	return out, nil
}
func requireWindowsNames(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows filename semantics")
	}
}

func TestInvalidEmptyDirectoryRejectedBeforeWrites(t *testing.T) {
	requireWindowsNames(t)
	for _, name := range []string{"trailing.", "trailing ", "CON", "bad:name", "outer/bad."} {
		t.Run(name, func(t *testing.T) {
			entries := map[string]Entry{name: {IsDir: true}, "valid": {IsDir: true}}
			if strings.Contains(name, "/") {
				entries["outer"] = Entry{IsDir: true}
			}
			src := Endpoint{FS: &windowsNameSource{entries: entries}, Root: "/source"}
			_, dst := endpoints(t)
			dst.Root = dst.At("not-created")
			o := opts()
			o.CreateEmptyDirs = true
			if _, err := BuildPlan(context.Background(), src, dst, "copy", o); err == nil {
				t.Fatal("unsupported directory accepted")
			}
			if _, err := os.Stat(dst.Root); !os.IsNotExist(err) {
				t.Fatal("planning changed destination", err)
			}
			o.Filter, _ = NewFilter(nil, []string{name}, "")
			if _, err := BuildPlan(context.Background(), src, dst, "copy", o); err != nil {
				t.Fatal("excluded invalid directory blocked plan", err)
			}
		})
	}
}

func TestExcludedCaseConflictDoesNotBlockCopy(t *testing.T) {
	requireWindowsNames(t)
	for _, mode := range []string{"copy", "sync"} {
		for _, emptyDirs := range []bool{false, true} {
			s, d := endpoints(t)
			write(t, s, "ignored/a", "ignored")
			write(t, d, "IGNORED/a", "retain")
			write(t, s, "keep.txt", "keep")
			o := opts()
			o.CreateEmptyDirs = emptyDirs
			o.Filter, _ = NewFilter(nil, []string{"ignored", "IGNORED"}, "")
			p, err := BuildPlan(context.Background(), s, d, mode, o)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Execute(context.Background(), p, o); err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{"IGNORED/a": "retain", "keep.txt": "keep"} {
				got, err := os.ReadFile(d.At(name))
				if err != nil || string(got) != want {
					t.Fatal(name, string(got), err)
				}
			}
		}
	}
}

func TestSelectedParentStillChecksExcludedDestination(t *testing.T) {
	requireWindowsNames(t)
	s, d := endpoints(t)
	write(t, s, "parent/selected.txt", "data")
	write(t, d, "PARENT/keep.txt", "retain")
	for _, filesOnly := range []bool{false, true} {
		o := opts()
		if filesOnly {
			o.Filter = Filter{Files: map[string]bool{"parent/selected.txt": true}}
		} else {
			o.Filter, _ = NewFilter([]string{"*.txt"}, []string{"PARENT"}, "")
		}
		if _, err := BuildPlan(context.Background(), s, d, "copy", o); err == nil || !strings.Contains(err.Error(), "case collision") {
			t.Fatal("selected parent aliases destination", err)
		}
	}
}

func TestSelectedSourceDirectoryCaseCollision(t *testing.T) {
	requireWindowsNames(t)
	for _, emptyDirs := range []bool{false, true} {
		entries := map[string]Entry{"Folder": {IsDir: true}, "folder": {IsDir: true}}
		if !emptyDirs {
			entries["Folder/a.txt"] = Entry{}
			entries["folder/b.txt"] = Entry{}
		}
		src := Endpoint{FS: &windowsNameSource{entries: entries}, Root: "/source"}
		_, dst := endpoints(t)
		o := opts()
		o.CreateEmptyDirs = emptyDirs
		if _, err := BuildPlan(context.Background(), src, dst, "copy", o); err == nil || !strings.Contains(err.Error(), "case collision") {
			t.Fatal("source collision accepted", err)
		}
	}
}
