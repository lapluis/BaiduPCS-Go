package pcsupload

import (
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestResumeFilesWithoutMD5RemainIndependent(t *testing.T) {
	for _, digest := range [][]byte{nil, {}, {1}} {
		dir := t.TempDir()
		db := &UploadingDatabase{}
		meta := func(name string) *checksum.LocalFileMeta {
			return &checksum.LocalFileMeta{Path: filepath.Join(dir, name), Length: 7, ModTime: -1, MD5: digest}
		}
		for _, name := range []string{"a", "b"} {
			db.UpdateUploading(meta(name), &uploader.InstanceState{Uploadid: name})
		}
		if len(db.UploadingList) != 2 {
			t.Errorf("%d-byte digest: collapsed into %d records", len(digest), len(db.UploadingList))
			continue
		}
		db.UpdateFullBlock(meta("b"), &uploader.InstanceState{Uploadid: "b-updated"})
		for _, tc := range []struct{ name, id string }{{"a", "a"}, {"b", "b-updated"}} {
			found := db.Search(meta(tc.name))
			if found == nil || found.Uploadid != tc.id {
				t.Fatalf("%s resumed another file: %v", tc.name, found)
			}
		}
		if !db.Delete(meta("b")) {
			t.Fatal("could not remove b")
		}
		if found := db.Search(meta("a")); found == nil || found.Uploadid != "a" {
			t.Fatal("deleting b removed a's state")
		}
		if found := db.Search(meta("b")); found != nil {
			t.Fatal("b was not removed")
		}
	}
}
