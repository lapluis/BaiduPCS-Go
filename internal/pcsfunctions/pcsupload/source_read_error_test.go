package pcsupload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type taskFaultingSource struct {
	rio.ReaderAtLen64
	failure error
}

func (r taskFaultingSource) ReadAt(p []byte, off int64) (int, error) {
	if off >= 4 {
		return 0, r.failure
	}
	return r.ReaderAtLen64.ReadAt(p, off)
}

type taskSourceFailureBackend struct {
	digestResumeBackend
	calls map[int]int
}

func (b *taskSourceFailureBackend) TmpFile(ctx context.Context, id, target string, part int, offset int64, r rio.ReaderLen64) (string, error) {
	b.calls[part]++
	if b.calls[part] > 1 {
		return "", &uploader.MultiError{Err: errors.New("unexpected part retry"), Terminated: true}
	}
	sum, err := b.digestResumeBackend.TmpFile(ctx, id, target, part, offset, r)
	if err != nil {
		wrapped := pcserror.NewPCSErrorInfo("fixture part")
		wrapped.SetNetError(err)
		return sum, wrapped
	}
	return sum, nil
}

func TestUploadTaskSourceFailureDoesNotRetryAndSavesProgress(t *testing.T) {
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
			failure := errors.New("fixture source read failure")
			backend := &taskSourceFailureBackend{digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}, calls: make(map[int]int)}
			source := taskFaultingSource{rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), failure}
			u := uploader.NewMultiUploader(backend, source, &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			u.SetInstanceState(task.state)
			result := task.executeUploader(u)
			if result.Succeed || result.NeedRetry || !errors.Is(result.Err, uploader.ErrSourceRead) || !errors.Is(result.Err, failure) || errors.Is(result.Err, ErrCheckpointSave) != failSave || backend.merged || backend.calls[0] != 1 || backend.calls[1] != 1 {
				t.Fatalf("source failure lost or retried: result=%+v calls=%v merged=%v", result, backend.calls, backend.merged)
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
		})
	}
}
