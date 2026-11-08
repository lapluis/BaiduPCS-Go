package rc

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestDirectoryOnlyIncludesSelectEmptyDirectories(t *testing.T) {
	source, target := endpoints(t)
	write(t, source, "secret", "ordinary file")
	for _, name := range []string{"parent/secret", "other"} {
		if err := os.MkdirAll(source.At(name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	o := opts()
	o.CreateEmptyDirs = true
	var err error
	o.Filter, err = NewFilter([]string{"**/secret/"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	// Files-from retains root-relative keys without a trailing separator.
	o.Filter.Files = map[string]bool{"parent/secret": true, "secret": true}
	plan, err := BuildPlan(context.Background(), source, target, "copy", o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Execute(context.Background(), plan, o); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(target.At("parent/secret")); err != nil || !info.IsDir() {
		t.Fatalf("included empty directory missing: %v", err)
	}
	for _, name := range []string{"secret", "other"} {
		if _, err := os.Stat(target.At(name)); !os.IsNotExist(err) {
			t.Errorf("unselected entry copied: %s err=%v", name, err)
		}
	}
}

func TestDirectoryOnlyExclusionsKeepOrdinaryFiles(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		for _, pattern := range []string{"*/", "**/secret/"} {
			t.Run(mode+"/"+pattern, func(t *testing.T) {
				source, target := endpoints(t)
				write(t, source, "secret", "ordinary file content")
				write(t, source, "other.txt", "other file content")
				write(t, source, "parent/secret/keep.txt", "protected directory content")
				for _, name := range []string{"another/secret", "eligible/empty"} {
					if err := os.MkdirAll(source.At(name), 0700); err != nil {
						t.Fatal(err)
					}
				}
				o := opts()
				o.CreateEmptyDirs, o.DeleteEmptyDirs = true, true
				var err error
				o.Filter, err = NewFilter(nil, []string{pattern}, "")
				if err != nil {
					t.Fatal(err)
				}
				plan, err := BuildPlan(context.Background(), source, target, mode, o)
				if err != nil {
					t.Fatal(err)
				}
				result, err := Execute(context.Background(), plan, o)
				if err != nil || result.Copied != 2 {
					t.Errorf("ordinary files were excluded by directory rule: result=%+v err=%v", result, err)
				}
				for name, want := range map[string]string{"secret": "ordinary file content", "other.txt": "other file content"} {
					if data, err := os.ReadFile(target.At(name)); err != nil || string(data) != want {
						t.Errorf("ordinary file not copied: %s data=%q err=%v", name, data, err)
					}
					data, err := os.ReadFile(source.At(name))
					if mode == "move" {
						if !os.IsNotExist(err) {
							t.Errorf("selected move source retained: %s err=%v", name, err)
						}
					} else if err != nil || string(data) != want {
						t.Errorf("copy/sync source changed: %s data=%q err=%v", name, data, err)
					}
				}
				if data, err := os.ReadFile(source.At("parent/secret/keep.txt")); err != nil || string(data) != "protected directory content" {
					t.Errorf("excluded content changed: data=%q err=%v", data, err)
				}
				for _, name := range []string{"parent/secret", "another/secret"} {
					if info, err := os.Stat(source.At(name)); err != nil || !info.IsDir() {
						t.Errorf("excluded directory removed: %s err=%v", name, err)
					}
					if _, err := os.Stat(target.At(name)); !os.IsNotExist(err) {
						t.Errorf("excluded directory copied: %s err=%v", name, err)
					}
				}
				wantEmptyTarget := pattern != "*/"
				if _, err := os.Stat(target.At("eligible/empty")); (err == nil) != wantEmptyTarget || err != nil && !os.IsNotExist(err) {
					t.Errorf("empty-directory selection changed: pattern=%q want=%t err=%v", pattern, wantEmptyTarget, err)
				}
			})
		}
	}
}

func TestDirectoryOnlyExclusionDoesNotProtectOrdinaryDeleteTargets(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			source, _ := endpoints(t)
			write(t, source, "secret", "ordinary file content")
			write(t, source, "parent/secret/keep.txt", "protected directory content")
			o := opts()
			o.DryRun = dry
			var err error
			o.Filter, err = NewFilter(nil, []string{"**/secret/"}, "")
			if err != nil {
				t.Fatal(err)
			}
			var removed []string
			o.Emit = func(kind, name string) {
				if kind == "delete" || kind == "would-delete" {
					removed = append(removed, name)
				}
			}
			if err := Delete(context.Background(), source, "delete", o); err != nil {
				t.Fatal(err)
			}
			if len(removed) != 1 || removed[0] != "secret" {
				t.Errorf("directory-only exclusion selected wrong files: %v", removed)
			}
			data, err := os.ReadFile(source.At("secret"))
			if dry {
				if err != nil || string(data) != "ordinary file content" {
					t.Errorf("preview changed file: data=%q err=%v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Errorf("selected ordinary file was not deleted: %v", err)
			}
			if data, err := os.ReadFile(source.At("parent/secret/keep.txt")); err != nil || string(data) != "protected directory content" {
				t.Errorf("excluded descendant changed: data=%q err=%v", data, err)
			}
		})
	}
}
