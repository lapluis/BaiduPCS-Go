package pcsupload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func captureCheckpointCallbackPanic(run func() error) (err error, escaped any) {
	defer func() { escaped = recover() }()
	err = run()
	return
}

func TestCheckpointObserverPanicPreservesSaveFailure(t *testing.T) {
	for _, phase := range []string{"direct", "cancel", "complete", "periodic", "healthy"} {
		for _, typed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, typed), func(t *testing.T) {
				t.Parallel()
				task, database, original, cleanup := checkpointFailureTask(t)
				defer cleanup()
				if phase == "healthy" {
					task.UploadingDatabase.Close()
					db, err := NewUploadingDatabaseAt(database)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					task.UploadingDatabase = db
				}
				cause := errors.New("synthetic checkpoint observer panic")
				calls := 0
				var observed error
				task.OnTransferError = func(err error) {
					calls++
					observed = err
					if typed {
						panic(cause)
					}
					panic(cause.Error())
				}
				var result *taskframework.TaskUnitRunResult
				run := task.saveCheckpoint
				switch phase {
				case "cancel":
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					task.Context = ctx
					run = func() error { result = task.upload(); return result.Err }
				case "complete", "periodic":
					var backend uploader.MultiUpload = &digestResumeBackend{parts: make(map[int]string)}
					if phase == "periodic" {
						backend = &checkpointBlockedBackend{digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}, started: make(chan struct{})}
					}
					u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
					u.SetInstanceState(task.state)
					run = func() error { result = task.executeUploader(u); return result.Err }
				}
				err, escaped := captureCheckpointCallbackPanic(run)
				if escaped != nil {
					t.Fatalf("checkpoint observer escaped: %v", escaped)
				}
				if phase == "healthy" {
					if err != nil || calls != 0 {
						t.Fatalf("healthy save invoked error callback: %v calls=%d", err, calls)
					}
					return
				}
				var pathErr *os.PathError
				if !errors.Is(err, ErrCheckpointSave) || !errors.As(err, &pathErr) || !errors.Is(observed, ErrCheckpointSave) || calls == 0 {
					t.Fatalf("original save failure lost: err=%v observed=%v calls=%d", err, observed, calls)
				}
				if !strings.Contains(err.Error(), cause.Error()) || (typed && !errors.Is(err, cause)) {
					t.Fatalf("observer panic cause lost: %v", err)
				}
				if errors.Is(err, context.Canceled) != (phase == "cancel") || errors.Is(err, uploader.ErrProgressCallback) {
					t.Errorf("observer failure changed cancellation/progress classification: %v", err)
				}
				if result != nil && (result.Succeed || result.NeedRetry) {
					t.Fatalf("save failure treated as success/retry: %+v", result)
				}
				if got, err := os.ReadFile(database); err != nil || string(got) != string(original) {
					t.Fatalf("failed save changed original checkpoint: %q %v", got, err)
				}
				if got, err := os.ReadFile(task.LocalFileChecksum.Path); err != nil || string(got) != "abcdefgh" {
					t.Fatalf("observer changed source: %q %v", got, err)
				}
			})
		}
	}
}

func TestUploadCancellationRetainsCauseWithCheckpointResult(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		for _, saveFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%t/%t", deadline, saveFails), func(t *testing.T) {
				task, database, _, cleanup := checkpointFailureTask(t)
				defer cleanup()
				if !saveFails {
					task.UploadingDatabase.Close()
					db, err := NewUploadingDatabaseAt(database)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					task.UploadingDatabase = db
				}
				ctx, cancel := context.WithCancel(context.Background())
				if deadline {
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				}
				cancel()
				task.Context = ctx
				result := task.upload()
				if result.Succeed || result.NeedRetry || !errors.Is(result.Err, ctx.Err()) || errors.Is(result.Err, ErrCheckpointSave) != saveFails {
					t.Fatalf("cancellation cause lost: %+v", result)
				}
			})
		}
	}
}
