package rc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDownloadSharedPartialReplacementFailureRetainsProgress(t *testing.T) {
	f := newStageReadFixture(t)
	backup := filepath.Join(t.TempDir(), "backup.bin")
	if err := os.Link(f.stage.payload, backup); err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(f.stage.payload)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocker := os.NewFile(uintptr(handle), f.stage.payload)
	defer blocker.Close()
	_, err = stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
	var renameErr *os.LinkError
	if !errors.As(err, &renameErr) || renameErr.Op != "rename" {
		t.Fatalf("expected replacement failure while partial disallowed deletion: %v", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	f.checkPreservedAndRetry(t)
	if data, err := os.ReadFile(backup); err != nil || string(data) != "partial" {
		t.Errorf("failed replacement changed backup: %q %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Dir(f.stage.payload))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".detach-") {
			t.Errorf("temporary clone leaked: %s", entry.Name())
		}
	}
}
