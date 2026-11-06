package pcsupload

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadDatabaseReadOnlySavePreservesPreviousRecord(t *testing.T) {
	name := filepath.Join(t.TempDir(), "resume.json")
	original := []byte(`{"upload_state":[],"timestamp":123}`)
	if err := os.WriteFile(name, original, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(name)
	if err != nil {
		t.Fatal(err)
	}
	db.openFile = func(path string, _ int, _ os.FileMode) (*os.File, error) { return os.Open(path) }
	defer db.Close()
	var pathErr *os.PathError
	if err := db.Save(); !errors.As(err, &pathErr) {
		t.Fatalf("read-only handle did not report an I/O error: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != string(original) {
		t.Fatalf("failed save changed the previous record: %q err=%v", got, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(name), ".pcs-upload-state-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("failed save leaked temporary files", leftovers, err)
	}
}
