package rc

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDownloadStageLockedRecordRetainsProgress(t *testing.T) {
	f := newStageReadFixture(t)
	path, err := windows.UTF16PtrFromString(f.stage.record)
	if err != nil {
		t.Fatal(err)
	}
	// Deny reads but allow deletion, reproducing a transient read failure that
	// must not authorize discarding otherwise valid multi-file resume state.
	handle, err := windows.CreateFile(path, windows.GENERIC_WRITE, windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocker := os.NewFile(uintptr(handle), f.stage.record)
	defer blocker.Close()
	if _, err := os.ReadFile(f.stage.record); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("record was not read-locked: %v", err)
	}
	_, err = stageFor(context.Background(), f.remote, "/file", f.target, f.entry)
	if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Errorf("read lock not reported: %v", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	f.checkPreservedAndRetry(t)
}
