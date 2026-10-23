package checksum

import (
	"bytes"
	"crypto/md5"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathInvalidatesPreviousDigests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("aaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 2)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	if err := checksum.Sum(CHECKSUM_MD5 | CHECKSUM_SLICE_MD5 | CHECKSUM_CRC32); err != nil {
		t.Fatal(err)
	}
	if err := checksum.CalculateChunkedSum(2); err != nil {
		t.Fatal(err)
	}
	oldFile := checksum.GetFile()
	if err := os.WriteFile(path, []byte("bbbb"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	if checksum.MD5 != nil || checksum.SliceMD5 != nil || checksum.BlocksList != nil || checksum.CRC32 != 0 {
		t.Fatal("reopened file retained previous digests")
	}
	if _, err := oldFile.Stat(); err == nil {
		t.Fatal("previous file handle remained open")
	}
	if err := checksum.Sum(CHECKSUM_MD5); err != nil {
		t.Fatal(err)
	}
	want := md5.Sum([]byte("bbbb"))
	if !bytes.Equal(checksum.MD5, want[:]) {
		t.Fatal("new file contents were not hashed")
	}
}

func TestFailedOpenPathDoesNotExposeOldMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 2)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	if err := checksum.Sum(CHECKSUM_MD5); err != nil {
		t.Fatal(err)
	}
	oldFile := checksum.GetFile()
	checksum.Path = filepath.Join(t.TempDir(), "missing.bin")
	if err := checksum.OpenPath(); err == nil {
		t.Fatal("missing file opened")
	}
	if checksum.GetFile() != nil || checksum.MD5 != nil || checksum.Length != 0 {
		t.Fatal("failed open retained old file or metadata")
	}
	if _, err := oldFile.Stat(); err == nil {
		t.Fatal("failed reopen leaked old handle")
	}
}
