package pcsupload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestCanceledUploadReportsCheckpointFailure(t *testing.T) {
	task, database, original, cleanup := checkpointFailureTask(t)
	defer cleanup()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task.Context = ctx
	reported := false
	task.OnTransferError = func(err error) { reported = errors.Is(err, ErrCheckpointSave) }
	result := task.upload()
	var pathErr *os.PathError
	if result.Succeed || result.NeedRetry || !errors.As(result.Err, &pathErr) || !errors.Is(result.Err, ErrCheckpointSave) || !reported {
		t.Fatalf("checkpoint failure hidden by cancellation: %+v reported=%v", result, reported)
	}
	data, err := os.ReadFile(database)
	if err != nil || string(data) != string(original) {
		t.Fatal("failed save destroyed previous record", err)
	}
}

func TestCompletedUploadReportsCheckpointCleanupFailure(t *testing.T) {
	task, _, _, cleanup := checkpointFailureTask(t)
	defer cleanup()
	backend := &digestResumeBackend{parts: make(map[int]string)}
	muer := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	muer.SetInstanceState(task.state)
	result := task.executeUploader(muer)
	if !backend.merged || result.Succeed || result.NeedRetry || !errors.Is(result.Err, ErrCheckpointSave) || result.ResultMessage != "清理上传进度失败" {
		t.Fatalf("cleanup failure reported as successful upload: merged=%v result=%+v", backend.merged, result)
	}
}

type checkpointBlockedBackend struct {
	digestResumeBackend
	started chan struct{}
}

func (b *checkpointBlockedBackend) TmpFile(ctx context.Context, id, target string, part int, offset int64, r rio.ReaderLen64) (string, error) {
	if part == 1 {
		close(b.started)
		<-ctx.Done()
		return "", ctx.Err()
	}
	return b.digestResumeBackend.TmpFile(ctx, id, target, part, offset, r)
}

func TestPeriodicUploadCheckpointFailureStopsBeforeMerge(t *testing.T) {
	task, database, original, cleanup := checkpointFailureTask(t)
	defer cleanup()
	backend := &checkpointBlockedBackend{digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}, started: make(chan struct{})}
	muer := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	muer.SetInstanceState(task.state)
	done := make(chan struct{})
	var result *taskframework.TaskUnitRunResult
	go func() { result = task.executeUploader(muer); close(done) }()
	defer func() { muer.Cancel(); <-done }()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("second part did not start")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("periodic checkpoint failure did not stop the upload")
	}
	if result.Succeed || result.NeedRetry || !errors.Is(result.Err, ErrCheckpointSave) || backend.merged {
		t.Fatalf("checkpoint failure did not prevent merge: merged=%v result=%+v", backend.merged, result)
	}
	data, err := os.ReadFile(database)
	if err != nil || string(data) != string(original) {
		t.Fatal("periodic save failure destroyed the previous checkpoint", err)
	}
}

func checkpointFailureTask(t *testing.T) (*UploadTaskUnit, string, []byte, func()) {
	t.Helper()
	root := t.TempDir()
	source, database := filepath.Join(root, "file"), filepath.Join(root, "state.json")
	if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"upload_state":[],"timestamp":123}`)
	if err := os.WriteFile(database, original, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := NewUploadingDatabaseAt(database)
	if err != nil {
		t.Fatal(err)
	}
	db.openFile = func(path string, _ int, _ os.FileMode) (*os.File, error) { return os.Open(path) }
	local := checksum.NewLocalFileChecksum(source, 4)
	if err := local.OpenPath(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	task := &UploadTaskUnit{LocalFileChecksum: local, UploadingDatabase: db, UploadStatistic: &UploadStatistic{}, Parallel: 1, SavePath: "/fixture", taskInfo: &taskframework.TaskInfo{}, state: &uploader.InstanceState{Uploadid: "fixture"}}
	return task, database, original, func() { local.Close(); db.Close() }
}
