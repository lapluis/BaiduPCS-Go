package pcsupload

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestUploadTaskProgressPanicSavesCheckpoint(t *testing.T) {
	for _, mode := range []string{"saved", "save-fails"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadTaskProgressPanicProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_UPLOAD_TASK_PROGRESS_PANIC="+mode)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("task progress failure lost checkpoint: %v %s", err, output)
			}
		})
	}
}

func TestUploadTaskProgressPanicProcessFixture(t *testing.T) {
	mode := os.Getenv("PCS_UPLOAD_TASK_PROGRESS_PANIC")
	if mode == "" {
		return
	}
	task, database, original, cleanup := checkpointFailureTask(t)
	defer cleanup()
	failSave := mode == "save-fails"
	if !failSave {
		task.UploadingDatabase.Close()
		db, err := NewUploadingDatabaseAt(database)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		task.UploadingDatabase = db
	}
	cause := errors.New("synthetic task upload progress panic")
	task.Progress = func(int64, int64, int64) { panic(cause) }
	reported := false
	task.OnTransferError = func(err error) { reported = errors.Is(err, ErrCheckpointSave) }
	backend := &checkpointBlockedBackend{digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}, started: make(chan struct{})}
	u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	u.SetInstanceState(task.state)
	result := task.executeUploader(u)
	if result.Succeed || result.NeedRetry || backend.merged || !errors.Is(result.Err, cause) || !errors.Is(result.Err, uploader.ErrProgressCallback) || errors.Is(result.Err, ErrCheckpointSave) != failSave || reported != failSave {
		t.Fatalf("progress failure not retained: result=%+v reported=%t merged=%t", result, reported, backend.merged)
	}
	if got, err := os.ReadFile(task.LocalFileChecksum.Path); err != nil || string(got) != "abcdefgh" {
		t.Fatalf("progress error changed source: %q %v", got, err)
	}
	if failSave {
		var pathErr *os.PathError
		if !errors.As(result.Err, &pathErr) {
			t.Fatal("checkpoint path error lost")
		}
		if got, err := os.ReadFile(database); err != nil || string(got) != string(original) {
			t.Fatalf("save failure destroyed old state: %q %v", got, err)
		}
		return
	}
	task.UploadingDatabase.Close()
	reloaded, err := NewUploadingDatabaseAt(database)
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	saved := reloaded.Search(&task.LocalFileChecksum.LocalFileMeta)
	if saved == nil || len(saved.BlockList) != 2 || saved.BlockList[0].CheckSum == "" || saved.BlockList[1].CheckSum != "" {
		t.Fatalf("completed part did not survive reload: %+v", saved)
	}
	task.UploadingDatabase = reloaded
	task.state = saved
	task.Progress = nil
	retryBackend := &digestResumeBackend{parts: make(map[int]string)}
	retry := uploader.NewMultiUploader(retryBackend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	retry.SetInstanceState(saved)
	result = task.executeUploader(retry)
	if !result.Succeed || result.Err != nil || !retryBackend.merged || len(retryBackend.parts) != 1 || retryBackend.parts[1] != "efgh" {
		t.Fatalf("resume reuploaded completed part: result=%+v parts=%v", result, retryBackend.parts)
	}
}
