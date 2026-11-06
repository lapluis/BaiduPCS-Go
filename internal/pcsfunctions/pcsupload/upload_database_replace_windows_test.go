package pcsupload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"golang.org/x/sys/windows"
)

func TestUploadDatabaseReplacementFailurePreservesPreviousRecord(t *testing.T) {
	name := filepath.Join(t.TempDir(), "resume.json")
	db, err := NewUploadingDatabaseAt(name)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta := &checksum.LocalFileMeta{Path: filepath.Join(t.TempDir(), "file"), Length: 4, ModTime: -1}
	db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "old"})
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}
	old, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	// Allow ordinary writes, but deny replacing the database until released.
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocker := os.NewFile(uintptr(h), name)
	defer blocker.Close()
	db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "new session with more progress"})
	if err := db.Save(); err == nil {
		t.Error("database was overwritten in place instead of reporting replacement failure")
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != string(old) {
		t.Error("replacement failure destroyed the previous record", err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(name), ".pcs-upload-state-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("failed save leaked temporary files", leftovers, err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(); err != nil {
		t.Fatal("retry failed after replacement lock released", err)
	}
	reloaded, err := NewUploadingDatabaseAt(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	state := reloaded.Search(meta)
	if state == nil || state.Uploadid != "new session with more progress" {
		t.Fatal("retry did not publish the latest progress")
	}
}
