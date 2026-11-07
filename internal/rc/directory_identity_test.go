package rc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func replaceEmptyDirectory(t *testing.T, name string) {
	t.Helper()
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, before) {
		t.Fatal("cannot capture original directory identity")
	}
	if err := os.Rename(name, filepath.Join(t.TempDir(), "old-directory")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(name, 0755); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(name)
	if err != nil || os.SameFile(before, after) {
		t.Fatalf("replacement fixture did not change identity: %v", err)
	}
}

func TestSyncRetainsReplacedDirectory(t *testing.T) {
	for _, timing := range []string{"before", "during"} {
		t.Run(timing, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, d, "a", "planned deletion")
			if err := os.Mkdir(d.At("z"), 0755); err != nil {
				t.Fatal(err)
			}
			o := opts()
			plan, err := BuildPlan(context.Background(), s, d, "sync", o)
			if err != nil {
				t.Fatal(err)
			}
			if timing == "before" {
				replaceEmptyDirectory(t, d.At("z"))
			} else {
				o.Emit = func(kind, p string) {
					if kind == "delete" && p == "a" {
						replaceEmptyDirectory(t, d.At("z"))
					}
				}
			}
			result, err := Execute(context.Background(), plan, o)
			if err == nil || !strings.Contains(err.Error(), "changed after planning") {
				t.Errorf("replacement accepted: result=%+v err=%v", result, err)
			}
			if info, err := os.Stat(d.At("z")); err != nil || !info.IsDir() {
				t.Errorf("new directory removed: %v", err)
			}
			if timing == "before" {
				if got, err := os.ReadFile(d.At("a")); err != nil || string(got) != "planned deletion" {
					t.Errorf("preflight failure deleted file: %q %v", got, err)
				}
			} else if result.Deleted != 1 {
				t.Errorf("completed deletion lost: %+v", result)
			}
		})
	}
}

func TestSingleFileMoveWithDirectoryCleanup(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "one", "only copy")
	s.Root, d.Root = s.At("one"), d.At("renamed")
	o := opts()
	o.DeleteEmptyDirs = true
	plan, err := FilePlan(context.Background(), s, d, true, o)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(context.Background(), plan, o)
	if err != nil || result.Copied != 1 {
		t.Fatalf("completed moveto reported failure: %+v %v", result, err)
	}
	if got, err := os.ReadFile(d.Root); err != nil || string(got) != "only copy" {
		t.Fatalf("destination lost: %q %v", got, err)
	}
	if _, err := os.Stat(s.Root); !os.IsNotExist(err) {
		t.Errorf("move retained source: %v", err)
	}
}

func TestMoveFileCommandWithDirectoryCleanup(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "one", "command data")
	p, err := parse([]string{s.At("one"), d.Root, "--delete-empty-src-dirs"})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateFlags("move", p); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), "move", p, &Resolver{CWD: s.Root}, io.Discard); err != nil {
		t.Fatal("single file move command failed:", err)
	}
	if got, err := os.ReadFile(d.At("one")); err != nil || string(got) != "command data" {
		t.Errorf("command destination=%q err=%v", got, err)
	}
	if _, err := os.Stat(s.At("one")); !os.IsNotExist(err) {
		t.Errorf("command retained source: %v", err)
	}
}

type replaceDirectoryOnList struct {
	Local
	target  string
	replace func()
	fired   bool
}

func (b *replaceDirectoryOnList) List(ctx context.Context, p string) ([]Entry, error) {
	items, err := b.Local.List(ctx, p)
	if err == nil && !b.fired && filepath.Clean(p) == filepath.Clean(b.target) {
		b.fired = true
		b.replace()
	}
	return items, err
}

func TestDirectoryReplacementDuringListingIsRetained(t *testing.T) {
	for _, mode := range []string{"rmdir", "rmdirs", "purge", "purge-root", "sync", "include-root"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			target := d.Root
			if mode == "rmdirs" || mode == "purge" || mode == "sync" {
				target = d.At("z")
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
			}
			var plan *Plan
			var err error
			if mode == "sync" {
				plan, err = BuildPlan(context.Background(), s, d, "sync", opts())
				if err != nil {
					t.Fatal(err)
				}
			}
			backend := &replaceDirectoryOnList{target: target, replace: func() { replaceEmptyDirectory(t, target) }}
			d.FS = backend
			switch mode {
			case "sync":
				plan.Destination = d
				_, err = Execute(context.Background(), plan, opts())
			case "include-root":
				err = removeEmpty(context.Background(), d, true, opts())
			case "purge-root":
				err = Delete(context.Background(), d, "purge", opts())
			default:
				err = Delete(context.Background(), d, mode, opts())
			}
			if !backend.fired || err == nil || !strings.Contains(err.Error(), "changed after planning") {
				t.Errorf("replacement accepted: fired=%t err=%v", backend.fired, err)
			}
			if info, err := os.Stat(target); err != nil || !info.IsDir() {
				t.Errorf("replacement directory lost: %v", err)
			}
		})
	}
}

func TestMoveDirectoryCleanupUsesOriginalPlan(t *testing.T) {
	for _, mode := range []string{"new", "new-child", "replaced", "normal"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, s, "one", "move data")
			if mode != "new" {
				if err := os.Mkdir(s.At("z"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			o := opts()
			o.DeleteEmptyDirs = true
			plan, err := BuildPlan(context.Background(), s, d, "move", o)
			if err != nil {
				t.Fatal(err)
			}
			o.Emit = func(kind, p string) {
				if kind != "copy" || p != "one" {
					return
				}
				switch mode {
				case "new":
					if err := os.Mkdir(s.At("z"), 0755); err != nil {
						t.Fatal(err)
					}
				case "new-child":
					if err := os.Mkdir(s.At("z/new"), 0755); err != nil {
						t.Fatal(err)
					}
				case "replaced":
					replaceEmptyDirectory(t, s.At("z"))
				}
			}
			result, err := Execute(context.Background(), plan, o)
			if (err != nil) != (mode == "replaced") || result.Copied != 1 {
				t.Errorf("wrong move result: %+v err=%v", result, err)
			}
			if got, err := os.ReadFile(d.At("one")); err != nil || string(got) != "move data" {
				t.Errorf("destination lost: %q %v", got, err)
			}
			if _, err := os.Stat(s.At("one")); !os.IsNotExist(err) {
				t.Errorf("moved file retained: %v", err)
			}
			info, statErr := os.Stat(s.At("z"))
			if mode == "normal" {
				if !os.IsNotExist(statErr) {
					t.Errorf("original empty directory retained: %v", statErr)
				}
			} else if statErr != nil || !info.IsDir() {
				t.Errorf("new directory removed: %v", statErr)
			}
			if mode == "new-child" {
				if _, err := os.Stat(s.At("z/new")); err != nil {
					t.Errorf("unplanned child removed: %v", err)
				}
			}
		})
	}
}

func TestDirectoryCleanupAllowsOwnChildChanges(t *testing.T) {
	for _, mode := range []string{"purge", "sync", "rmdirs", "move"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			target := d
			if mode == "move" {
				target = s
			}
			if err := os.MkdirAll(target.At("a/b"), 0755); err != nil {
				t.Fatal(err)
			}
			if mode != "rmdirs" {
				write(t, target, "a/b/file", "content")
			}
			o := opts()
			o.DeleteEmptyDirs = true
			var err error
			if mode == "sync" || mode == "move" {
				var plan *Plan
				plan, err = BuildPlan(context.Background(), s, d, mode, o)
				if err == nil {
					_, err = Execute(context.Background(), plan, o)
				}
			} else {
				err = Delete(context.Background(), d, mode, o)
			}
			if err != nil {
				t.Fatal("own child changes blocked cleanup:", err)
			}
			if _, err := os.Stat(target.At("a")); !os.IsNotExist(err) {
				t.Errorf("empty tree retained: %v", err)
			}
			if mode == "move" {
				if got, err := os.ReadFile(d.At("a/b/file")); err != nil || string(got) != "content" {
					t.Errorf("moved data lost: %q %v", got, err)
				}
			}
		})
	}
}
