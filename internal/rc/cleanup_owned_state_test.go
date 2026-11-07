package rc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestDirectoryRemovalPreservesHiddenDownloadState(t *testing.T) {
	for _, mode := range []string{"rmdir", "rmdirs", "sync", "move"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%t", mode, dry), func(t *testing.T) {
				s, d := endpoints(t)
				owner := d
				if mode == "move" {
					owner = s
				}
				state := filepath.Join(owner.At("protected"), ".pcsclone-partials")
				if err := os.MkdirAll(filepath.Dir(state), 0700); err != nil {
					t.Fatal(err)
				}
				if err := prepareStageDirectory(state); err != nil {
					t.Fatal(err)
				}
				partial := filepath.Join(state, "partial.part")
				if err := os.WriteFile(partial, []byte("saved progress"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(owner.At("eligible"), 0700); err != nil {
					t.Fatal(err)
				}
				o := opts()
				o.DryRun, o.DeleteEmptyDirs = dry, true
				var events []string
				o.Emit = func(kind, name string) {
					if kind == "rmdir" || kind == "would-rmdir" {
						events = append(events, name)
					}
				}
				var err error
				if mode == "sync" || mode == "move" {
					var plan *Plan
					plan, err = BuildPlan(context.Background(), s, d, mode, o)
					if err == nil {
						_, err = Execute(context.Background(), plan, o)
					}
				} else {
					endpoint := owner
					if mode == "rmdir" {
						endpoint.Root = owner.At("protected")
					}
					err = Delete(context.Background(), endpoint, mode, o)
				}
				if mode == "rmdir" {
					if err == nil || len(events) != 0 {
						t.Errorf("nonempty directory accepted: events=%v err=%v", events, err)
					}
				} else if err != nil || !reflect.DeepEqual(events, []string{"eligible"}) {
					t.Errorf("cleanup treated hidden state as empty: events=%v err=%v", events, err)
				}
				if data, err := os.ReadFile(partial); err != nil || string(data) != "saved progress" {
					t.Fatalf("download progress changed: data=%q err=%v", data, err)
				}
				if !ownedStageDirectory(state) {
					t.Fatal("state marker changed")
				}
				_, statErr := os.Stat(owner.At("eligible"))
				if dry || mode == "rmdir" {
					if statErr != nil {
						t.Errorf("preview changed eligible directory: %v", statErr)
					}
				} else if !os.IsNotExist(statErr) {
					t.Errorf("unrelated empty directory not cleaned: %v", statErr)
				}
			})
		}
	}
}

func TestSyncCleanupPreviewSimulatesNestedRemovals(t *testing.T) {
	s, d := endpoints(t)
	write(t, d, "extra/nested/file", "obsolete content")
	o := opts()
	plan, err := BuildPlan(context.Background(), s, d, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	var preview, actual []string
	o.DryRun = true
	o.Emit = func(kind, name string) {
		if kind == "would-rmdir" {
			preview = append(preview, name)
		}
	}
	if _, err := Execute(context.Background(), plan, o); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(d.At("extra/nested/file")); err != nil || string(data) != "obsolete content" {
		t.Fatalf("preview changed destination: data=%q err=%v", data, err)
	}
	o.DryRun = false
	o.Emit = func(kind, name string) {
		if kind == "rmdir" {
			actual = append(actual, name)
		}
	}
	if _, err := Execute(context.Background(), plan, o); err != nil {
		t.Fatal(err)
	}
	sort.Strings(preview)
	sort.Strings(actual)
	want := []string{"extra", "extra/nested"}
	if !reflect.DeepEqual(preview, want) || !reflect.DeepEqual(actual, want) {
		t.Fatalf("preview disagrees with cleanup: preview=%v actual=%v", preview, actual)
	}
	if _, err := os.Stat(d.At("extra")); !os.IsNotExist(err) {
		t.Fatalf("obsolete tree remains: %v", err)
	}
}
