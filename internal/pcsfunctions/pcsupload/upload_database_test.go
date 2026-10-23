package pcsupload

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"os"
	"path/filepath"
	"testing"
)

func TestResumeDatabaseRejectsInvalidJSON(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resume.json")
	data := []byte(`{"upload_state":[`)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(file)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Fatal("invalid resume JSON silently accepted")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(data) {
		t.Fatal("invalid state changed", err)
	}
	if err = os.Rename(file, file+".saved"); err != nil {
		t.Fatal("failed open leaked handle", err)
	}
}
func TestResumeDatabaseNullRecords(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resume.json")
	if err := os.WriteFile(file, []byte(`{"upload_state":[null,{}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta := &checksum.LocalFileMeta{Path: filepath.Join(t.TempDir(), "file"), ModTime: -1, Length: 4}
	if got := db.Search(meta); got != nil {
		t.Fatal("empty records returned state")
	}
	db.UpdateUploading(meta, nil)
	if !db.Delete(meta) {
		t.Fatal("usable record not removable")
	}
	if err = db.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestResumeDatabaseRejectsNullBlock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resume.json")
	if err := os.WriteFile(file, []byte(`{"upload_state":[{"path":"synthetic","state":{"block_list":[null]}}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(file)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Fatal("null upload block accepted")
	}
	if err = os.Rename(file, file+".saved"); err != nil {
		t.Fatal("handle leaked", err)
	}
}

func TestResumeDatabaseRejectsChecksumsWithoutState(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resume.json")
	if err := os.WriteFile(file, []byte(`{"upload_state":[{"path":"synthetic","blocklist":["checksum"],"state":null}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(file)
	if db != nil {
		db.Close()
	}
	if err == nil {
		t.Fatal("missing upload state accepted")
	}
}
func TestResumeDatabaseValidStateRoundTrip(t *testing.T) {
	file := filepath.Join(t.TempDir(), "resume.json")
	data := []byte(`{"upload_state":[{"path":"synthetic","length":4,"modtime":-1,"state":{"uploadid":"fixture","block_list":[]}}]}`)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Save(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	db, err = NewUploadingDatabaseAt(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(db.UploadingList) != 1 || db.UploadingList[0].State.Uploadid != "fixture" {
		t.Fatal("valid state lost")
	}
}
