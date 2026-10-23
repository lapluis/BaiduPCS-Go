package pcsupload

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestResumeSubsecondTimestampSurvivesSaveAndReload(t *testing.T) {
	for _, changed := range []bool{false, true} {
		dir := t.TempDir()
		path, database := filepath.Join(dir, "fixture.bin"), filepath.Join(dir, "resume.json")
		stamp := time.Unix(1700000000, 100000000)
		if err := os.WriteFile(path, []byte("aaaa"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		old, err := checksum.GetFileSum(path, checksum.CHECKSUM_MD5|checksum.CHECKSUM_SLICE_MD5)
		if err != nil {
			t.Fatal(err)
		}
		db, err := NewUploadingDatabaseAt(database)
		if err != nil {
			t.Fatal(err)
		}
		db.UpdateUploading(&old.LocalFileMeta, &uploader.InstanceState{Uploadid: "fixture"})
		if err := db.Save(); err != nil {
			db.Close()
			t.Fatal(err)
		}
		db.Close()
		if changed {
			if err := os.WriteFile(path, []byte("bbbb"), 0600); err != nil {
				t.Fatal(err)
			}
			stamp = stamp.Add(700 * time.Millisecond)
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
		db, err = NewUploadingDatabaseAt(database)
		if err != nil {
			t.Fatal(err)
		}
		current := checksum.NewLocalFileChecksum(path, 2)
		if err := current.OpenPath(); err != nil {
			db.Close()
			t.Fatal(err)
		}
		found := db.Search(&current.LocalFileMeta)
		if changed && (found != nil || current.MD5 != nil) {
			t.Error("same-second rewrite resumed old contents")
		}
		if !changed && (found == nil || found.Uploadid != "fixture") {
			t.Error("unchanged file did not resume")
		}
		current.Close()
		db.Close()
	}
}

func TestLegacyResumeTimestampNeedsVerifiedDigest(t *testing.T) {
	for _, verified := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "fixture.bin")
		if err := os.WriteFile(path, []byte("aaaa"), 0600); err != nil {
			t.Fatal(err)
		}
		old, err := checksum.GetFileSum(path, checksum.CHECKSUM_MD5)
		if err != nil {
			t.Fatal(err)
		}
		// Explicitly construct the old schema without a subsecond timestamp.
		legacy := &checksum.LocalFileMeta{Path: path, Length: old.Length, ModTime: old.ModTime, MD5: old.MD5}
		db := &UploadingDatabase{}
		db.UpdateUploading(legacy, &uploader.InstanceState{Uploadid: "legacy"})
		query := &checksum.LocalFileMeta{Path: path, Length: old.Length}
		if verified {
			query.MD5 = old.MD5
		}
		found := db.Search(query)
		if verified && (found == nil || found.Uploadid != "legacy") {
			t.Error("verified legacy record did not resume")
		}
		if !verified && found != nil {
			t.Error("trusted an ambiguous legacy timestamp without content verification")
		}
	}
}
