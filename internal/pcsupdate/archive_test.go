package pcsupdate

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func archiveFixture(t *testing.T, names ...string) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		w, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte("new")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}
func TestArchivePreflightRejectsUnsafePaths(t *testing.T) {
	for _, bad := range []string{"release/../../outside", "/absolute", "release/../program", "release/C:/program", `release\program`, "release/./program", "release/program"} {
		dir := t.TempDir()
		target := filepath.Join(dir, "program")
		if err := os.WriteFile(target, []byte("old"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := installArchive(archiveFixture(t, "release/program", bad), dir, target); err == nil {
			t.Errorf("unsafe path accepted: %s", bad)
		}
		data, _ := os.ReadFile(target)
		if string(data) != "old" {
			t.Fatal("validation failure changed original")
		}
	}
}
func TestArchiveInstallsValidFileAndRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "renamed-program")
	if err := os.WriteFile(target, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := installArchive(archiveFixture(t), dir, target); err == nil {
		t.Fatal("empty archive accepted")
	}
	if err := installArchive(archiveFixture(t, "release/BaiduPCS-Go"), dir, target); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "new" {
		t.Fatal("valid update not installed")
	}
}

func TestArchiveReadFailurePreservesAllOriginals(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	reader := archiveFixture(t, "release/one", "release/two")
	reader.File[1].CRC32++ // Fail after decoding the later entry's contents.
	if err := installArchive(reader, dir, filepath.Join(dir, "one")); err == nil {
		t.Fatal("corrupt archive accepted")
	}
	for _, name := range []string{"one", "two"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "original" {
			t.Errorf("corrupt later entry changed %s", name)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatal("archive staging files leaked")
	}
}
