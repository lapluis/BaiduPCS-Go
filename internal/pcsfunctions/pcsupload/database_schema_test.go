package pcsupload

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestUploadDatabaseRejectsUnreadableRecordsBeforeSaving(t *testing.T) {
	for _, mode := range []string{"null-block", "missing-state", "empty-digests-without-state", "healthy", "nil-entry", "unstarted"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, "state.json")
			db, err := NewUploadingDatabaseAt(name)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			first := &checksum.LocalFileMeta{Path: filepath.Join(root, "first"), Length: 8, ModTime: -1}
			db.UpdateUploading(first, &uploader.InstanceState{Uploadid: "first-session"})
			if err := db.Save(); err != nil {
				t.Fatal(err)
			}
			beforeDisk, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			beforeVersion := db.version
			db.Timestamp = 42
			second := &Uploading{LocalFileMeta: &checksum.LocalFileMeta{Path: filepath.Join(root, "second"), Length: 4, ModTime: -1}, State: &uploader.InstanceState{Uploadid: "second-session"}}
			switch mode {
			case "null-block":
				second.State.BlockList = []*uploader.BlockState{nil}
			case "missing-state":
				second.State = nil
				second.BlocksList = []string{"cached digest"}
			case "empty-digests-without-state":
				second.State = nil
				second.BlocksList = []string{}
			case "nil-entry":
				second = nil
			case "unstarted":
				second.State = nil
			}
			db.UploadingList = append(db.UploadingList, second)
			beforeMemory, err := json.Marshal(db)
			if err != nil {
				t.Fatal(err)
			}
			err = db.Save()
			invalid := mode == "null-block" || mode == "missing-state" || mode == "empty-digests-without-state"
			if invalid {
				if !errors.Is(err, ErrCheckpointSave) || !strings.Contains(err.Error(), "invalid upload resume database") {
					t.Errorf("unreadable record was saved or lost cause: %v", err)
				}
				if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, beforeDisk) {
					t.Errorf("invalid record replaced old database: bytes=%d error=%v", len(got), err)
				}
				afterMemory, err := json.Marshal(db)
				if err != nil || !bytes.Equal(afterMemory, beforeMemory) || db.version != beforeVersion || db.Timestamp != 42 {
					t.Errorf("failed validation changed in-memory metadata: bytes=%d error=%v", len(afterMemory), err)
				}
			} else if err != nil {
				t.Fatalf("compatible record rejected: %v", err)
			}
			reloaded, err := NewUploadingDatabaseAt(name)
			if err != nil {
				t.Fatalf("database cannot be reopened after save: %v", err)
			}
			defer reloaded.Close()
			wantEntries := 2
			if invalid {
				wantEntries = 1
			}
			found := reloaded.Search(first)
			if found == nil || found.Uploadid != "first-session" || len(reloaded.UploadingList) != wantEntries {
				t.Fatalf("original session lost: %+v entries=%d", found, len(reloaded.UploadingList))
			}
			if invalid {
				db.UploadingList = db.UploadingList[:1]
				db.UpdateUploading(&checksum.LocalFileMeta{Path: filepath.Join(root, "fixed"), Length: 4, ModTime: -1}, &uploader.InstanceState{Uploadid: "recovered-session"})
				if err := db.Save(); err != nil {
					t.Fatalf("rejected input poisoned later valid save: %v", err)
				}
				fixed, err := NewUploadingDatabaseAt(name)
				if err != nil {
					t.Fatal(err)
				}
				defer fixed.Close()
				if len(fixed.UploadingList) != 2 || fixed.UploadingList[1].State.Uploadid != "recovered-session" {
					t.Fatal("valid recovery did not persist both sessions")
				}
			}
		})
	}
}
