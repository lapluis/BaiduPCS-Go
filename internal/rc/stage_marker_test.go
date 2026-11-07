package rc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStageDirectoryMarkerRequiresExactContent(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		want          bool
	}{
		{"valid", stageMarker, true},
		{"empty", "", false},
		{"truncated", strings.TrimSuffix(stageMarker, "\n"), false},
		{"trailing", stageMarker + "x", false},
		{"wrong", strings.Repeat("x", len(stageMarker)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, ".owner"), []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if got := ownedStageDirectory(dir); got != tc.want {
				t.Fatalf("ownership=%t want=%t", got, tc.want)
			}
		})
	}
	for _, directory := range []bool{false, true} {
		dir := t.TempDir()
		if directory {
			if err := os.Mkdir(filepath.Join(dir, ".owner"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if ownedStageDirectory(dir) {
			t.Fatal("missing or non-file marker accepted")
		}
	}
}

func TestLargeStageMarkerDoesNotAllocateFileSizedBuffer(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".pcsclone-partials")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, ".owner")
	f, err := os.Create(marker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(stageMarker); err != nil {
		f.Close()
		t.Fatal(err)
	}
	const size = 16 << 20
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// These tests are sequential. TotalAlloc counts Go heap allocation even if
	// the temporary ReadFile buffer is reclaimed by a concurrent GC cycle.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	owned := ownedStageDirectory(dir)
	runtime.ReadMemStats(&after)
	if owned {
		t.Fatal("valid prefix with trailing file data accepted")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("marker check allocated %d bytes for a %d-byte file", allocated, size)
	}
	info, err := os.Stat(marker)
	if err != nil || info.Size() != size {
		t.Fatalf("marker file modified: info=%v err=%v", info, err)
	}
	entries, err := (&Local{}).List(context.Background(), root)
	if err != nil || len(entries) != 1 || entries[0].Name != ".pcsclone-partials" || !entries[0].IsDir {
		t.Fatalf("ordinary user directory hidden: entries=%v err=%v", entries, err)
	}
}
