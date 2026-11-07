package rc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDownloadWorkerRejectsJunctionTargets(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing-file", true: "missing-parent"}[missing], func(t *testing.T) {
			link, physical := newListingRootJunction(t)
			target := filepath.Join(link, "file")
			if missing {
				target = filepath.Join(link, "missing", "data")
			}
			checkDownloadTargetWorker(t, target, false)
			if data, err := os.ReadFile(filepath.Join(physical, "file")); err != nil || string(data) != "fixture" {
				t.Fatalf("junction target was overwritten: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(physical, "missing")); !os.IsNotExist(err) {
				t.Fatalf("worker created a directory through junction: %v", err)
			}
		})
	}
}

func TestDownloadWorkerRechecksTargetAfterMetadata(t *testing.T) {
	link, physical := newListingRootJunction(t)
	parent := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "file"), []byte("original target"), 0600); err != nil {
		t.Fatal(err)
	}
	checkDownloadTargetWorker(t, filepath.Join(parent, "file"), false, func() {
		if err := os.Rename(parent, parent+".original"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Rename(link, parent); err != nil {
			t.Error(err)
		}
	})
	for name, want := range map[string]string{
		filepath.Join(physical, "file"):           "fixture",
		filepath.Join(parent+".original", "file"): "original target",
	} {
		if data, err := os.ReadFile(name); err != nil || string(data) != want {
			t.Fatalf("metadata-time replacement changed file: %q %v", data, err)
		}
	}
}
