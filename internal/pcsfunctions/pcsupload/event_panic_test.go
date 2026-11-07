package pcsupload

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestUploadTaskEventFailureRetainsCheckpoint(t *testing.T) {
	for _, phase := range []string{"execute", "finish", "finish-save-fails"} {
		t.Run(phase, func(t *testing.T) {
			task, database, original, cleanup := checkpointFailureTask(t)
			defer cleanup()
			failSave := phase == "finish-save-fails"
			if !failSave {
				task.UploadingDatabase.Close()
				db, err := NewUploadingDatabaseAt(database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				task.UploadingDatabase = db
			}
			backend := &digestResumeBackend{parts: make(map[int]string)}
			u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			u.SetInstanceState(task.state)
			cause := fmt.Errorf("synthetic upload %s event panic", phase)
			if phase == "execute" {
				u.OnExecute(func() { panic(cause) })
			} else {
				u.OnFinish(func() { panic(cause) })
			}
			result := task.executeUploader(u)
			if result.Succeed || result.NeedRetry || !errors.Is(result.Err, cause) || !errors.Is(result.Err, uploader.ErrEventCallback) || errors.Is(result.Err, ErrCheckpointSave) != failSave {
				t.Fatalf("task hid event failure: %+v", result)
			}
			if phase == "execute" && (backend.merged || len(backend.parts) != 0) {
				t.Fatal("startup event failure performed remote work")
			}
			if got, err := os.ReadFile(task.LocalFileChecksum.Path); err != nil || string(got) != "abcdefgh" {
				t.Fatalf("event failure changed source: %q %v", got, err)
			}
			if failSave {
				if got, err := os.ReadFile(database); err != nil || string(got) != string(original) {
					t.Fatalf("failed save changed old state: %q %v", got, err)
				}
				return
			}
			task.UploadingDatabase.Close()
			reloaded, err := NewUploadingDatabaseAt(database)
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.Close()
			state := reloaded.Search(&task.LocalFileChecksum.LocalFileMeta)
			if state == nil || len(state.BlockList) != 2 {
				t.Fatalf("event failure lost session: %+v", state)
			}
			for _, block := range state.BlockList {
				if (block.CheckSum != "") != (phase == "finish") {
					t.Fatalf("wrong completed state after event failure: %+v", state)
				}
			}
		})
	}
}
