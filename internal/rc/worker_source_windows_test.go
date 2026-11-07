package rc

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUploadWorkerRejectsJunctionSources(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "ancestor"}[ancestor], func(t *testing.T) {
			link, target := newListingRootJunction(t)
			source := link
			if ancestor {
				if err := os.WriteFile(filepath.Join(target, "file"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				source = filepath.Join(link, "file")
			}
			checkUploadSourceWorker(t, source, false)
			want := "fixture"
			if ancestor {
				want = ""
			}
			if data, err := os.ReadFile(filepath.Join(target, "file")); err != nil || string(data) != want {
				t.Fatalf("junction target changed: %q %v", data, err)
			}
		})
	}
}
