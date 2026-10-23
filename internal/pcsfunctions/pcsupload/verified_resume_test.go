package pcsupload

import (
	"bytes"
	"crypto/md5"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestResumeRejectsVerifiedHashMismatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("aaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := checksum.GetFileSum(path, checksum.CHECKSUM_MD5)
	if err != nil {
		t.Fatal(err)
	}
	db := &UploadingDatabase{}
	db.UpdateUploading(&old.LocalFileMeta, &uploader.InstanceState{Uploadid: "old-content"})
	if err := os.WriteFile(path, []byte("bbbb"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(old.ModTime, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	current, err := checksum.GetFileSum(path, checksum.CHECKSUM_MD5)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{}, current.MD5...)
	if found := db.Search(&current.LocalFileMeta); found != nil {
		t.Errorf("resumed old contents: %s", found.Uploadid)
	}
	if !bytes.Equal(current.MD5, want) {
		t.Error("lookup overwrote the verified current digest")
	}
	if len(db.UploadingList) != 0 {
		t.Error("mismatched resume record was not invalidated")
	}
}

func TestResumePreservesVerifiedDigestWhenStoredHashMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	db := &UploadingDatabase{}
	db.UpdateUploading(&checksum.LocalFileMeta{Path: path, Length: 4, ModTime: -1}, &uploader.InstanceState{Uploadid: "fixture"})
	hash := md5.Sum([]byte("aaaa"))
	query := &checksum.LocalFileMeta{Path: path, Length: 4, MD5: append([]byte{}, hash[:]...)}
	if found := db.Search(query); found == nil || found.Uploadid != "fixture" {
		t.Fatal("path-based resume failed")
	}
	if !bytes.Equal(query.MD5, hash[:]) {
		t.Fatal("missing cached digest overwrote current verified digest")
	}
}

func TestResumeMatchingVerifiedDigestStillWorks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	hash := md5.Sum([]byte("aaaa"))
	db := &UploadingDatabase{}
	db.UpdateUploading(&checksum.LocalFileMeta{Path: path, Length: 4, ModTime: -1, MD5: hash[:]}, &uploader.InstanceState{Uploadid: "fixture"})
	for _, queryPath := range []string{path, filepath.Join(t.TempDir(), "same-contents.bin")} {
		query := &checksum.LocalFileMeta{Path: queryPath, Length: 4, MD5: hash[:]}
		if found := db.Search(query); found == nil || found.Uploadid != "fixture" {
			t.Fatal("matching verified digest did not resume")
		}
	}
}
