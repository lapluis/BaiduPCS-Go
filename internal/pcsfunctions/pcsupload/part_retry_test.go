package pcsupload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type taskRetryFailureBackend struct {
	digestResumeBackend
	calls    map[int]int
	failure  error
	failPart int
}

func (b *taskRetryFailureBackend) TmpFile(ctx context.Context, id, target string, part int, offset int64, r rio.ReaderLen64) (string, error) {
	b.calls[part]++
	if part == b.failPart {
		if b.calls[part] > 3 {
			return "", &uploader.MultiError{Err: errors.New("unexpected extra retry"), Terminated: true}
		}
		return "", b.failure
	}
	return b.digestResumeBackend.TmpFile(ctx, id, target, part, offset, r)
}

func TestUploadTaskPartRetryLimitSavesProgress(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(fmt.Sprint(failSave), func(t *testing.T) {
			task, database, original, cleanup := checkpointFailureTask(t)
			defer cleanup()
			if !failSave {
				if err := task.UploadingDatabase.Close(); err != nil {
					t.Fatal(err)
				}
				db, err := NewUploadingDatabaseAt(database)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				task.UploadingDatabase = db
			}
			failure := errors.New("synthetic persistent request failure")
			failPart, wantFirst, wantSecond := 1, 1, 3
			if failSave {
				// No completed part notification: inject failure in the final
				// save, without racing the periodic checkpoint at three seconds.
				failPart, wantFirst, wantSecond = 0, 3, 0
			}
			backend := &taskRetryFailureBackend{digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}, calls: make(map[int]int), failure: failure, failPart: failPart}
			u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			u.SetInstanceState(task.state)
			result := task.executeUploader(u)
			if result.Succeed || result.NeedRetry || !errors.Is(result.Err, uploader.ErrPartRetryExhausted) || !errors.Is(result.Err, failure) || errors.Is(result.Err, ErrCheckpointSave) != failSave || errors.Is(result.Err, context.Canceled) || backend.merged || backend.calls[0] != wantFirst || backend.calls[1] != wantSecond {
				t.Fatalf("retry failure or saved state lost: result=%+v calls=%v merged=%v", result, backend.calls, backend.merged)
			}
			if failSave {
				data, err := os.ReadFile(database)
				if err != nil || string(data) != string(original) {
					t.Fatal("failed save changed old checkpoint", err)
				}
				return
			}
			if err := task.UploadingDatabase.Close(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := NewUploadingDatabaseAt(database)
			if err != nil {
				t.Fatal(err)
			}
			defer reloaded.Close()
			state := reloaded.Search(&task.LocalFileChecksum.LocalFileMeta)
			if state == nil || len(state.BlockList) != 2 || state.BlockList[0].CheckSum == "" || state.BlockList[1].CheckSum != "" {
				t.Fatalf("completed progress lost or failed part completed: %+v", state)
			}
			retryBackend := &digestResumeBackend{parts: make(map[int]string)}
			retry := uploader.NewMultiUploader(retryBackend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			retry.SetInstanceState(state)
			if err := retry.ExecuteWithError(); err != nil || len(retryBackend.parts) != 1 || retryBackend.parts[1] == "" || !retryBackend.merged {
				t.Fatalf("resume failed or resent completed part: parts=%v merged=%v err=%v", retryBackend.parts, retryBackend.merged, err)
			}
		})
	}
}
