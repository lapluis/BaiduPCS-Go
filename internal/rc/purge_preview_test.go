package rc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPurgePreviewRejectsHiddenRemainingState(t *testing.T) {
	for _, location := range []string{"", "nested"} {
		for _, dry := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dry=%t", location, dry), func(t *testing.T) {
				e, _ := endpoints(t)
				write(t, e, "visible", "ordinary data")
				parent := e.At(location)
				if err := os.MkdirAll(parent, 0700); err != nil {
					t.Fatal(err)
				}
				state := filepath.Join(parent, ".pcsclone-partials")
				if err := prepareStageDirectory(state); err != nil {
					t.Fatal(err)
				}
				partial := filepath.Join(state, "partial.part")
				if err := os.WriteFile(partial, []byte("checkpoint data"), 0600); err != nil {
					t.Fatal(err)
				}
				o := opts()
				o.DryRun = dry
				var directories []string
				o.Emit = func(kind, item string) {
					if kind == "would-rmdir" || kind == "rmdir" {
						directories = append(directories, item)
					}
				}
				if err := Delete(context.Background(), e, "purge", o); err == nil {
					t.Error("purge accepted a directory with retained state")
				}
				if len(directories) != 0 {
					t.Errorf("reported impossible directory removal: %v", directories)
				}
				if data, err := os.ReadFile(partial); err != nil || string(data) != "checkpoint data" || !ownedStageDirectory(state) {
					t.Fatalf("checkpoint changed: data=%q err=%v", data, err)
				}
				if dry {
					if data, err := os.ReadFile(e.At("visible")); err != nil || string(data) != "ordinary data" {
						t.Fatalf("preview changed visible file: data=%q err=%v", data, err)
					}
				} else if _, err := os.Stat(e.At("visible")); !os.IsNotExist(err) {
					t.Fatalf("actual purge behavior changed: %v", err)
				}
			})
		}
	}
}

func TestPurgePreviewRejectsDirectoryReplacement(t *testing.T) {
	for _, root := range []bool{false, true} {
		t.Run(fmt.Sprintf("root=%t", root), func(t *testing.T) {
			e, _ := endpoints(t)
			target := e.Root
			if !root {
				target = e.At("nested")
				if err := os.Mkdir(target, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(t, e, "file", "retained data")
			o := opts()
			o.DryRun = true
			replaced := false
			var directories []string
			o.Emit = func(kind, item string) {
				if kind == "would-delete" && !replaced {
					replaceEmptyDirectory(t, target)
					replaced = true
				}
				if kind == "would-rmdir" {
					directories = append(directories, item)
				}
			}
			err := Delete(context.Background(), e, "purge", o)
			if !replaced || err == nil || !strings.Contains(err.Error(), "changed after planning") || len(directories) != 0 {
				t.Fatalf("preview accepted replacement: replaced=%t directories=%v err=%v", replaced, directories, err)
			}
			if info, err := os.Stat(target); err != nil || !info.IsDir() {
				t.Fatalf("replacement was changed: %v", err)
			}
		})
	}
}

func TestPurgePreviewSimulatesNestedRemovals(t *testing.T) {
	e, _ := endpoints(t)
	write(t, e, "nested/deep/file", "ordinary data")
	o := opts()
	o.DryRun = true
	var preview, actual []string
	o.Emit = func(kind, item string) { preview = append(preview, kind+":"+item) }
	if err := Delete(context.Background(), e, "purge", o); err != nil {
		t.Fatal(err)
	}
	want := []string{"would-delete:nested/deep/file", "would-rmdir:nested/deep", "would-rmdir:nested", "would-rmdir:" + e.Root}
	if !reflect.DeepEqual(preview, want) {
		t.Fatalf("preview omitted planned removals: %v", preview)
	}
	if data, err := os.ReadFile(e.At("nested/deep/file")); err != nil || string(data) != "ordinary data" {
		t.Fatalf("preview changed file: data=%q err=%v", data, err)
	}
	o.DryRun = false
	o.Emit = func(kind, item string) { actual = append(actual, "would-"+kind+":"+item) }
	if err := Delete(context.Background(), e, "purge", o); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, preview) {
		t.Fatalf("actual events differ: preview=%v actual=%v", preview, actual)
	}
	if _, err := os.Stat(e.Root); !os.IsNotExist(err) {
		t.Fatalf("purge root remains: %v", err)
	}
}

func TestPurgePreviewRejectsReplacementDuringFinalListing(t *testing.T) {
	e, _ := endpoints(t)
	write(t, e, "file", "retained data")
	backend := &replaceDirectoryOnList{replace: func() { replaceEmptyDirectory(t, e.Root) }}
	e.FS = backend
	o := opts()
	o.DryRun = true
	directories := 0
	o.Emit = func(kind, _ string) {
		if kind == "would-delete" {
			backend.target = e.Root
		}
		if kind == "would-rmdir" {
			directories++
		}
	}
	err := Delete(context.Background(), e, "purge", o)
	if !backend.fired || err == nil || !strings.Contains(err.Error(), "changed after planning") || directories != 0 {
		t.Fatalf("listing replacement accepted: fired=%t directories=%d err=%v", backend.fired, directories, err)
	}
	if info, err := os.Stat(e.Root); err != nil || !info.IsDir() {
		t.Fatalf("replacement was changed: %v", err)
	}
}
