package pcsclone

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStorageCaseInspectionDoesNotCreateProbeFiles(t *testing.T) {
	dir := t.TempDir()
	if folded, err := storageDirectoryFoldsCase(dir); err == nil || folded || !strings.Contains(err.Error(), "case behavior") {
		t.Fatalf("empty directory was guessed: folded=%t err=%v", folded, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("case inspection modified directory: entries=%v err=%v", entries, err)
	}
}

func TestAmbiguousFutureStorageCaseDoesNotGuessOrCreateFiles(t *testing.T) {
	for _, missingParents := range []bool{false, true} {
		dir := t.TempDir()
		a, b := filepath.Join(dir, "state.json"), filepath.Join(dir, "STATE.json")
		if missingParents {
			a, b = filepath.Join(dir, "future", "state.json"), filepath.Join(dir, "FUTURE", "STATE.json")
		}
		same, err := sameStoragePath(a, b)
		if runtime.GOOS == "windows" {
			if !same || err != nil {
				t.Errorf("Windows path policy changed: same=%t err=%v", same, err)
			}
		} else if same || err == nil || !strings.Contains(err.Error(), "case behavior") {
			t.Errorf("future case behavior was guessed: same=%t err=%v", same, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("comparison modified directory: entries=%v err=%v", entries, err)
		}
	}
}

func TestStorageCaseInspectionKeepsDistinctHardLinkNames(t *testing.T) {
	dir := t.TempDir()
	lower, upper := filepath.Join(dir, "anchor.txt"), filepath.Join(dir, "Anchor.txt")
	if err := os.WriteFile(lower, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(upper); err == nil {
		t.Skip("directory already folds filename case")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Link(lower, upper); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if folded, err := storageDirectoryFoldsCase(dir); err != nil || folded {
		t.Fatalf("distinct hard-link entries confused with folded lookup: folded=%t err=%v", folded, err)
	}
}

func TestStorageCaseKeepsDistinctExistingDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses its existing conservative path policy")
	}
	dir := t.TempDir()
	lower, upper := filepath.Join(dir, "lower"), filepath.Join(dir, "LOWER")
	if err := os.Mkdir(lower, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(upper); err == nil {
		t.Skip("directory already folds filename case")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Mkdir(upper, 0700); err != nil {
		t.Fatal(err)
	}
	same, err := sameStoragePath(filepath.Join(lower, "settings.json"), filepath.Join(upper, "settings.json"))
	if err != nil || same {
		t.Fatalf("distinct future files were rejected: same=%t err=%v", same, err)
	}
}

func TestStorageCaseRejectsChangingReferenceFile(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			dir := t.TempDir()
			anchor, alternate := filepath.Join(dir, "anchor.txt"), filepath.Join(dir, "Anchor.txt")
			if err := os.WriteFile(anchor, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			changed := false
			stat := func(path string) (os.FileInfo, error) {
				if path == alternate && !changed {
					changed = true
					if err := os.Rename(anchor, filepath.Join(dir, "retained.txt")); err != nil {
						t.Fatal(err)
					}
					if replace {
						if err := os.WriteFile(anchor, []byte("replacement"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				return os.Lstat(path)
			}
			folded, err := inspectStorageDirectoryCase(dir, stat)
			if !changed || err == nil || folded {
				t.Errorf("changed reference was used as case evidence: changed=%t folded=%t err=%v", changed, folded, err)
			}
			if data, err := os.ReadFile(filepath.Join(dir, "retained.txt")); err != nil || string(data) != "original" {
				t.Errorf("original reference lost: data=%q err=%v", data, err)
			}
		})
	}
}
