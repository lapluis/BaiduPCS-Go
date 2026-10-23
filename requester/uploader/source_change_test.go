package uploader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type changingSourceBackend struct {
	resumeBackend
	stage       string
	mutate      func() error
	mutationErr error
}

func (b *changingSourceBackend) Precreate() (string, pcserror.Error) {
	if b.stage == "precreate" {
		b.mutationErr = b.mutate()
	}
	return b.resumeBackend.Precreate()
}

func (b *changingSourceBackend) TmpFile(ctx context.Context, uploadid, path string, id int, offset int64, reader rio.ReaderLen64) (string, error) {
	checksum, err := b.resumeBackend.TmpFile(ctx, uploadid, path, id, offset, reader)
	if err == nil && id == 0 && b.stage == "part" {
		if err := b.mutate(); err != nil {
			return "", &MultiError{Err: err, Terminated: true}
		}
	}
	return checksum, err
}

func (b *changingSourceBackend) CreateSuperFile(host, policy, id string, size int64, sums map[int]string) error {
	if b.stage == "merge" {
		if err := b.mutate(); err != nil {
			return err
		}
	}
	return b.resumeBackend.CreateSuperFile(host, policy, id, size, sums)
}

func TestUploadRejectsSourceChanges(t *testing.T) {
	for _, stage := range []string{"before_execute", "precreate", "part", "merge", "completed_resume"} {
		for _, contents := range []string{"ccccdddd", "ccccdddd-extra", "cc"} {
			t.Run(stage+"/"+contents, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "fixture.bin")
				stamp := time.Unix(1700000000, 100000000)
				if err := os.WriteFile(path, []byte("aaaabbbb"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				file, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				backend := &changingSourceBackend{stage: stage, mutate: func() error {
					if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
						return err
					}
					changed := stamp.Add(700 * time.Millisecond)
					return os.Chtimes(path, changed, changed)
				}}
				u := NewMultiUploader(backend, rio.NewFileReaderAtLen64(file), &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
				if stage == "before_execute" {
					if err := backend.mutate(); err != nil {
						t.Fatal(err)
					}
				}
				if stage == "completed_resume" {
					backend.stage = "precreate"
					u.SetInstanceState(&InstanceState{Uploadid: "fixture", BlockList: []*BlockState{
						{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: "old-0"},
						{ID: 1, Range: transfer.Range{Begin: 4, End: 8}, CheckSum: "old-1"},
					}})
				}
				var uploadErr error
				success := false
				u.OnError(func(err error) { uploadErr = err })
				u.OnSuccess(func() { success = true })
				u.Execute()
				if backend.mutationErr != nil {
					t.Fatal(backend.mutationErr)
				}
				if success || !errors.Is(uploadErr, rio.ErrSourceChanged) {
					t.Errorf("changed source reported success: success=%v error=%v", success, uploadErr)
				}
				if stage != "merge" && backend.merged != 0 {
					t.Error("changed source was merged")
				}
				if stage == "before_execute" && backend.precreated != 0 {
					t.Error("source changed before execution still reached backend")
				}
				if stage == "part" && backend.uploaded != 1 {
					t.Errorf("dispatched extra parts after source changed: %d", backend.uploaded)
				}
			})
		}
	}
}

func TestUploadStableFileSnapshotStillSucceeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("aaaabbbb"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	backend := &resumeBackend{}
	u := NewMultiUploader(backend, rio.NewFileReaderAtLen64(file), &MultiUploaderConfig{Parallel: 2, BlockSize: 4}, "/fixture")
	success := false
	u.OnSuccess(func() { success = true })
	u.OnError(func(err error) { t.Errorf("unchanged source rejected: %v", err) })
	u.Execute()
	if !success || backend.merged != 1 || backend.uploaded != 2 {
		t.Fatalf("unexpected result: success=%v uploaded=%d merged=%d", success, backend.uploaded, backend.merged)
	}
}

func TestUploadClosedSourceNeverReachesBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reader := rio.NewFileReaderAtLen64(file)
	file.Close()
	backend := &resumeBackend{}
	u := NewMultiUploader(backend, reader, nil, "/fixture")
	var uploadErr error
	u.OnError(func(err error) { uploadErr = err })
	u.Execute()
	if !errors.Is(uploadErr, rio.ErrSourceChanged) || backend.precreated != 0 {
		t.Fatalf("closed source: error=%v precreated=%d", uploadErr, backend.precreated)
	}
}

func TestUploadPreservesOriginalChecksumSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("aaaabbbb"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 100000000)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	source := checksum.NewLocalFileChecksum(path, 2)
	if err := source.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err := source.Sum(checksum.CHECKSUM_MD5); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("ccccdddd"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp = stamp.Add(700 * time.Millisecond)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	// The upload reader is constructed after the mutation and sees a stable
	// new file, so it must also validate the earlier checksum snapshot.
	backend := &resumeBackend{}
	u := NewMultiUploader(backend, rio.NewFileReaderAtLen64(source.GetFile()), &MultiUploaderConfig{ValidateSource: source.ValidateSnapshot}, "/fixture")
	var uploadErr error
	u.OnError(func(err error) { uploadErr = err })
	u.Execute()
	if !errors.Is(uploadErr, rio.ErrSourceChanged) || backend.precreated != 0 {
		t.Fatalf("original snapshot ignored: err=%v backend calls=%d", uploadErr, backend.precreated)
	}
}
