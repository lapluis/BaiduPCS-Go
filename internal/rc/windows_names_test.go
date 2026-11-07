package rc

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Synthetic remote inventory permits names Windows cannot faithfully create.
type windowsNameSource struct {
	Local
	entries map[string]Entry
}

func TestWindowsLocalNameDeviceAliasesAndControls(t *testing.T) {
	requireWindowsNames(t)
	for _, name := range []string{"CONIN$", "conout$", "COM¹", "com².bin", "LPT³", "LPT² .txt", "CON .txt", "nul   .log", "outer/COM¹/file"} {
		t.Run(name, func(t *testing.T) {
			if err := validLocalName(name); err == nil {
				t.Fatal("device alias accepted")
			}
		})
	}
	for control := 0; control < 32; control++ {
		t.Run(fmt.Sprintf("control-%02x", control), func(t *testing.T) {
			if err := validLocalName("outer/a" + string(rune(control)) + "b"); err == nil {
				t.Fatal("control character accepted")
			}
		})
	}
	for _, name := range []string{".hidden", ".pcsclone-partials", ".CON", "COM0", "LPT0.txt", "COM10", "LPT¹x", "COM⁴", "CONIN$x", "archive.tar.gz", "字幕/第一集.mkv", "a b.txt", "a .txt"} {
		t.Run(name, func(t *testing.T) {
			if err := validLocalName(name); err != nil {
				t.Fatal("ordinary name rejected", err)
			}
		})
	}
}

func TestWindowsInvalidFilePlansRejectBeforeDestinationAccess(t *testing.T) {
	requireWindowsNames(t)
	for _, name := range []string{"CONIN$", "CONOUT$.bin", "COM¹", "LPT² .txt", "CON .txt", "bad\x01name"} {
		for _, move := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/move=%t", name, move), func(t *testing.T) {
				source := Endpoint{FS: &windowsNameSource{entries: map[string]Entry{"file": {Size: 8}}}, Root: "/source/file"}
				root := t.TempDir()
				destination := Endpoint{FS: &Local{}, Root: filepath.Join(root, "not-created", name)}
				_, err := FilePlan(context.Background(), source, destination, move, opts())
				if err == nil || (!strings.Contains(err.Error(), "reserved Windows filename") && !strings.Contains(err.Error(), "unsupported Windows filename")) {
					t.Fatal("invalid path reached destination access", err)
				}
				if _, err := os.Stat(filepath.Join(root, "not-created")); !os.IsNotExist(err) {
					t.Fatal("planning created destination", err)
				}
			})
		}
	}
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
	for _, name := range []string{"trailing.", "trailing ", "CON", "bad:name", "outer/bad.", "COM¹", "LPT²", "com³.txt", "CONIN$", "CONOUT$", "CON .txt", "outer/CONIN$", "bad\x01name", "bad\x00name"} {
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
