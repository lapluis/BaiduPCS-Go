package pcsutil_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil"
)

func walkLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
}

func walkFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestWalkDirRejectsDirectoryCycles(t *testing.T) {
	for _, kind := range []string{"self", "ancestor", "mutual"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			walkFile(t, filepath.Join(root, "a.txt"), "keep")
			switch kind {
			case "self":
				walkLink(t, ".", filepath.Join(root, "z-loop"))
			case "ancestor":
				sub := filepath.Join(root, "nested")
				if err := os.Mkdir(sub, 0700); err != nil {
					t.Fatal(err)
				}
				walkLink(t, "..", filepath.Join(sub, "back"))
			case "mutual":
				other := t.TempDir()
				walkLink(t, other, filepath.Join(root, "z-other"))
				walkLink(t, root, filepath.Join(other, "back"))
			}
			files, err := pcsutil.WalkDir(root, ".txt")
			if err == nil {
				t.Fatal("directory cycle accepted")
			}
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Op != "walk" {
				t.Fatalf("cycle was not reported as a traversal error: %v", err)
			}
			if len(files) != 1 || files[0] != filepath.Join(root, "a.txt") {
				t.Fatalf("cycle traversed the same directory again: %v", files)
			}
		})
	}
}

func TestWalkDirRejectsFileLinkCycles(t *testing.T) {
	for _, mutual := range []bool{false, true} {
		t.Run(map[bool]string{false: "self", true: "mutual"}[mutual], func(t *testing.T) {
			root := t.TempDir()
			walkFile(t, filepath.Join(root, "a.txt"), "fixture")
			first := filepath.Join(root, "z-loop.txt")
			if mutual {
				walkLink(t, "z-other.txt", first)
				walkLink(t, "z-loop.txt", filepath.Join(root, "z-other.txt"))
			} else {
				walkLink(t, "z-loop.txt", first)
			}
			files, err := pcsutil.WalkDir(root, ".txt")
			if err == nil {
				t.Fatalf("file link cycle silently skipped: %v", files)
			}
		})
	}
}

func TestWalkDirFileAndMissingRoots(t *testing.T) {
	root := t.TempDir()
	for _, content := range []string{"", "payload"} {
		name := filepath.Join(root, "data.TXT")
		walkFile(t, name, content)
		files, err := pcsutil.WalkDir(name, ".txt")
		wantCount := 0
		if content != "" {
			wantCount = 1
		}
		if err != nil || len(files) != wantCount || (wantCount == 1 && files[0] != name) {
			t.Fatalf("file root %q: files=%v err=%v", content, files, err)
		}
	}
	files, err := pcsutil.WalkDir(filepath.Join(root, "missing"), "")
	if !errors.Is(err, os.ErrNotExist) || len(files) != 0 {
		t.Fatalf("missing root: files=%v err=%v", files, err)
	}
}

func TestWalkDirLinkAliasesRemainIndependent(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	walkFile(t, filepath.Join(target, "data.TXT"), "payload")
	walkFile(t, filepath.Join(target, "empty.txt"), "")
	walkFile(t, filepath.Join(target, "other.bin"), "ignored")
	walkLink(t, target, filepath.Join(root, "first"))
	walkLink(t, target, filepath.Join(root, "second"))
	walkLink(t, filepath.Join(target, "data.TXT"), filepath.Join(root, "file.txt"))
	walkLink(t, filepath.Join(target, "empty.txt"), filepath.Join(root, "empty.txt"))
	walkLink(t, filepath.Join(target, "missing"), filepath.Join(root, "broken.txt"))
	want := []string{
		filepath.Join(root, "file.txt"),
		filepath.Join(root, "first", "data.TXT"),
		filepath.Join(root, "second", "data.TXT"),
	}
	files, err := pcsutil.WalkDir(root, ".txt")
	sort.Strings(files)
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("linked files = %v, %v; want %v", files, err, want)
	}
	// A linked directory is also a valid traversal root.
	files, err = pcsutil.WalkDir(filepath.Join(root, "first"), ".txt")
	if err != nil || !reflect.DeepEqual(files, want[1:2]) {
		t.Fatalf("linked root = %v, %v", files, err)
	}
}
