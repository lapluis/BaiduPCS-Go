package pcsupload

import (
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
	"path/filepath"
	"sync"
	"testing"
)

func TestResumeDatabaseOwnsSnapshots(t *testing.T) {
	db, err := NewUploadingDatabaseAt(filepath.Join(t.TempDir(), "resume.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	meta := &checksum.LocalFileMeta{Path: filepath.Join(t.TempDir(), "file"), Length: 4, ModTime: -1, MD5: []byte{1}, SliceMD5: []byte{2}, BlocksList: []string{"original"}}
	state := &uploader.InstanceState{Uploadid: "original", BlockList: []*uploader.BlockState{{CheckSum: "original"}}}
	originalPath := meta.Path
	db.UpdateUploading(meta, state)
	meta.Length = 99
	meta.MD5[0] = 9
	meta.SliceMD5[0] = 9
	meta.BlocksList[0] = "changed"
	state.Uploadid = "changed"
	state.BlockList[0].CheckSum = "changed"
	query := &checksum.LocalFileMeta{Path: originalPath, Length: 4}
	found := db.Search(query)
	if found == nil || found.Uploadid != "original" || found.BlockList[0].CheckSum != "original" || query.MD5[0] != 1 || query.SliceMD5[0] != 2 || query.BlocksList[0] != "original" {
		t.Fatal("database aliased input snapshots")
	}
	found.Uploadid = "caller"
	found.BlockList[0].CheckSum = "caller"
	query.MD5[0] = 8
	query.BlocksList[0] = "caller"
	query = &checksum.LocalFileMeta{Path: originalPath, Length: 4}
	found = db.Search(query)
	if found == nil || found.Uploadid != "original" || found.BlockList[0].CheckSum != "original" || query.MD5[0] != 1 || query.BlocksList[0] != "original" {
		t.Fatal("database aliased search result")
	}
}

func TestResumeDatabaseConcurrentOperations(t *testing.T) {
	db, err := NewUploadingDatabaseAt(filepath.Join(t.TempDir(), "resume.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			name := filepath.Join(root, fmt.Sprint(worker))
			for i := 0; i < 30; i++ {
				meta := &checksum.LocalFileMeta{Path: name, Length: int64(worker + 1), ModTime: -1}
				state := &uploader.InstanceState{Uploadid: fmt.Sprint(worker)}
				db.UpdateUploading(meta, state)
				found := db.Search(&checksum.LocalFileMeta{Path: name, Length: meta.Length})
				if found == nil || found.Uploadid != state.Uploadid {
					t.Errorf("worker %d lost state", worker)
					return
				}
				if err := db.Save(); err != nil {
					t.Error(err)
					return
				}
				// Size mismatch must remove this entry without recursively locking.
				if found = db.Search(&checksum.LocalFileMeta{Path: name, Length: 999}); found != nil {
					t.Error("size mismatch resumed")
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Save(); err == nil {
		t.Fatal("save after close succeeded")
	}
}

func TestResumeDatabaseUpdateReplacesMetadata(t *testing.T) {
	db := &UploadingDatabase{}
	meta := &checksum.LocalFileMeta{Path: filepath.Join(t.TempDir(), "file"), Length: 4, ModTime: -1}
	db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "old"})
	meta.Length = 8
	db.UpdateUploading(meta, &uploader.InstanceState{Uploadid: "new"})
	got := db.Search(&checksum.LocalFileMeta{Path: meta.Path, Length: 8})
	if got == nil || got.Uploadid != "new" {
		t.Fatal("updated state still used stale metadata")
	}
}

func TestResumeDatabaseFullBlockIsolation(t *testing.T) {
	db := &UploadingDatabase{}
	meta := &checksum.LocalFileMeta{Path: filepath.Join(t.TempDir(), "file"), Length: 4, ModTime: -1, BlocksList: []string{"hash"}}
	state := &uploader.InstanceState{Uploadid: "id", BlockList: []*uploader.BlockState{{}}}
	db.UpdateUploading(meta, state)
	for _, invalid := range []*uploader.InstanceState{nil, {}, {BlockList: []*uploader.BlockState{nil}}} {
		db.UpdateFullBlock(meta, invalid)
	}
	db.UpdateFullBlock(meta, state)
	if state.BlockList[0].CheckSum != "" {
		t.Fatal("caller state mutated")
	}
	got := db.Search(&checksum.LocalFileMeta{Path: meta.Path, Length: 4})
	if got == nil || got.BlockList[0].CheckSum != "hash" {
		t.Fatal("block checksums not saved")
	}
}
