package rc

import (
	"context"
	"os"
	"path"
	"runtime"
	"testing"
)

func TestSyncRecursiveExclusionPreservesNewlineFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain newlines; in-memory planning is tested separately")
	}
	for _, tc := range []struct{ name, protected, pattern string }{
		{"directory", "line\nbreak/keep.txt", "**/keep.txt"},
		{"filename", "keep/line\nbreak.txt", "keep/**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := endpoints(t)
			write(t, dst, tc.protected, "protected content")
			write(t, dst, "extra.txt", "delete this file")
			o := opts()
			var err error
			o.Filter, err = NewFilter(nil, []string{tc.pattern}, "")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildPlan(context.Background(), src, dst, "sync", o)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Execute(context.Background(), plan, o)
			if err != nil || result != (Result{Deleted: 1}) {
				t.Fatalf("sync result=%+v err=%v", result, err)
			}
			if data, err := os.ReadFile(dst.At(tc.protected)); err != nil || string(data) != "protected content" {
				t.Fatalf("recursive exclusion lost protected file: data=%q err=%v", data, err)
			}
			if _, err := os.Stat(dst.At("extra.txt")); !os.IsNotExist(err) {
				t.Fatalf("selected extra file was not removed: %v", err)
			}
		})
	}
}

func TestRecursiveFiltersMatchNewlines(t *testing.T) {
	for _, tc := range []struct{ pattern, name string }{
		{"**", "line\nbreak.txt"},
		{"**/*.txt", "line\nbreak/file.txt"},
		{"keep/**", "keep/line\nbreak.txt"},
		{"keep/**/file.txt", "keep/line\nbreak/deeper/file.txt"},
		{"**/file.txt", "file.txt"},
	} {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			included, err := NewFilter([]string{tc.pattern}, nil, "")
			if err != nil || !included.Match(tc.name) {
				t.Fatalf("recursive include missed filename: err=%v", err)
			}
			excluded, err := NewFilter(nil, []string{tc.pattern}, "")
			if err != nil || excluded.Match(tc.name) {
				t.Fatalf("recursive exclude missed filename: err=%v", err)
			}
		})
	}
}

// These names are valid on Unix and remote backends, even on a Windows host.
// Keep the fixture in memory so Windows filename restrictions cannot hide it.
type newlineFilterTree struct {
	Backend
	name  string
	items map[string][]Entry
}

func (f *newlineFilterTree) Identity() string { return f.name }
func (f *newlineFilterTree) Stat(context.Context, string) (Entry, error) {
	return Entry{IsDir: true}, nil
}
func (f *newlineFilterTree) List(_ context.Context, p string) ([]Entry, error) {
	return f.items[path.Clean(p)], nil
}

func TestRecursiveExclusionProtectsSyncDeletionWithNewline(t *testing.T) {
	src := Endpoint{FS: &newlineFilterTree{name: "empty", items: map[string][]Entry{}}, Root: "/root"}
	dst := Endpoint{FS: &newlineFilterTree{name: "target", items: map[string][]Entry{
		"/root":             {{Name: "line\nbreak", IsDir: true}},
		"/root/line\nbreak": {{Name: "keep.txt", Size: 4}},
	}}, Root: "/root"}
	for _, deleteExcluded := range []bool{false, true} {
		o := opts()
		o.DeleteExcluded = deleteExcluded
		var err error
		o.Filter, err = NewFilter(nil, []string{"**/keep.txt"}, "")
		if err != nil {
			t.Fatal(err)
		}
		plan, err := BuildPlan(context.Background(), src, dst, "sync", o)
		if err != nil {
			t.Fatal(err)
		}
		deletes := 0
		for _, action := range plan.Actions {
			if action.Kind == "delete" {
				deletes++
				if action.Path != "line\nbreak/keep.txt" {
					t.Fatalf("unexpected deletion: %+v", action)
				}
			}
		}
		want := 0
		if deleteExcluded {
			want = 1
		}
		if deletes != want {
			t.Fatalf("delete-excluded=%t: planned %d file deletions, want %d", deleteExcluded, deletes, want)
		}
	}
}
