package rc

import (
	"context"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestMoveDirectoryCleanupPreviewMatchesExecution(t *testing.T) {
	for _, mode := range []string{"copy", "identical", "ignored", "newer", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, s, "selected/file", "source contents")
			for _, name := range []string{"empty/nested", "excluded/nested"} {
				if err := os.MkdirAll(s.At(name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "identical" || mode == "ignored" || mode == "newer" {
				contents := "old target"
				if mode == "identical" {
					contents = "source contents"
				}
				write(t, d, "selected/file", contents)
				if mode == "newer" {
					future := time.Now().Add(time.Hour)
					if err := os.Chtimes(d.At("selected/file"), future, future); err != nil {
						t.Fatal(err)
					}
				}
			}
			o := opts()
			o.DeleteEmptyDirs = mode != "disabled"
			o.IgnoreExisting, o.Update = mode == "ignored", mode == "newer"
			var err error
			o.Filter, err = NewFilter(nil, []string{"excluded/"}, "")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildPlan(context.Background(), s, d, "move", o)
			if err != nil {
				t.Fatal(err)
			}
			var preview, actual []string
			o.DryRun = true
			o.Emit = func(kind, item string) {
				if kind == "would-rmdir" {
					preview = append(preview, item)
				}
			}
			if _, err := Execute(context.Background(), plan, o); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(s.At("selected/file")); err != nil || string(data) != "source contents" {
				t.Fatalf("preview changed source: data=%q err=%v", data, err)
			}
			for _, name := range []string{"selected", "empty/nested", "excluded/nested"} {
				if info, err := os.Stat(s.At(name)); err != nil || !info.IsDir() {
					t.Fatalf("preview removed source directory %s: %v", name, err)
				}
			}
			o.DryRun = false
			o.Emit = func(kind, item string) {
				if kind == "rmdir" {
					actual = append(actual, item)
				}
			}
			if _, err := Execute(context.Background(), plan, o); err != nil {
				t.Fatal(err)
			}
			var want []string
			if mode != "disabled" {
				want = []string{"empty", "empty/nested"}
				if mode == "copy" || mode == "identical" {
					want = append(want, "selected")
				}
			}
			sort.Strings(preview)
			sort.Strings(actual)
			sort.Strings(want)
			if !reflect.DeepEqual(preview, want) || !reflect.DeepEqual(actual, want) {
				t.Fatalf("preview disagrees with cleanup: preview=%v actual=%v want=%v", preview, actual, want)
			}
			if info, err := os.Stat(s.At("excluded/nested")); err != nil || !info.IsDir() {
				t.Fatalf("excluded directory removed: %v", err)
			}
		})
	}
}
