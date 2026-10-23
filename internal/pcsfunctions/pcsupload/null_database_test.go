package pcsupload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResumeDatabaseRejectsNullDocument(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "resume.json")
	if err := os.WriteFile(filename, []byte("null\n"), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(filename)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Fatal("null database accepted as empty state")
	}
	data, err := os.ReadFile(filename)
	if err != nil || string(data) != "null\n" {
		t.Fatal("invalid database changed")
	}
	if err := os.Rename(filename, filename+".saved"); err != nil {
		t.Fatal("failed load leaked file handle:", err)
	}
}
