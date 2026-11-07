package pcsupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type wrappedPolicyBackend struct {
	digestResumeBackend
	phase      string
	err        error
	partCalls  int
	mergeCalls int
}

func (b *wrappedPolicyBackend) TmpFile(ctx context.Context, id, path string, seq int, offset int64, r rio.ReaderLen64) (string, error) {
	b.partCalls++
	if b.phase == "part" {
		return "", b.err
	}
	return b.digestResumeBackend.TmpFile(ctx, id, path, seq, offset, r)
}

func (b *wrappedPolicyBackend) CreateSuperFile(context.Context, string, string, string, int64, map[int]string) error {
	b.mergeCalls++
	return b.err
}

func TestWrappedUploadErrorsRetainPolicyAndCauses(t *testing.T) {
	for _, phase := range []string{"part", "merge"} {
		for _, wrapping := range []string{"wrapped", "single-join", "independent-join"} {
			joined := wrapping == "independent-join"
			for _, tc := range []struct {
				name, policy string
				code         int
				skip, retry  bool
			}{
				{"skip", baidupcs.SkipPolicy, 114514, true, false},
				{"rsync", baidupcs.RsyncPolicy, 1919810, true, false},
				{"conflict-skip", baidupcs.SkipPolicy, 31061, true, false},
				{"conflict-overwrite", baidupcs.OverWritePolicy, 31061, false, false},
				{"quota", baidupcs.OverWritePolicy, 31112, false, true},
				{"expired", baidupcs.OverWritePolicy, 31363, false, true},
				{"network", baidupcs.OverWritePolicy, 0, false, true},
				{"http413", baidupcs.OverWritePolicy, 0, false, false},
				{"source-read", baidupcs.SkipPolicy, 0, false, false},
				{"source-and-skip", baidupcs.SkipPolicy, 114514, false, false},
			} {
				t.Run(fmt.Sprintf("%s/%s/%s", phase, tc.name, wrapping), func(t *testing.T) {
					root := t.TempDir()
					source, checkpoint := filepath.Join(root, "source"), filepath.Join(root, "state.json")
					if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
						t.Fatal(err)
					}
					local := checksum.NewLocalFileChecksum(source, 4)
					if err := local.OpenPath(); err != nil {
						t.Fatal(err)
					}
					defer local.Close()
					db, err := NewUploadingDatabaseAt(checkpoint)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					state := &uploader.InstanceState{Uploadid: "keep-session"}
					db.UpdateUploading(&local.LocalFileMeta, state)
					db.UpdateUploading(&checksum.LocalFileMeta{Path: filepath.Join(root, "other"), Length: 1, ModTime: -1}, &uploader.InstanceState{Uploadid: "other-session"})
					if err := db.Save(); err != nil {
						t.Fatal(err)
					}
					beforeDisk, err := os.ReadFile(checkpoint)
					if err != nil {
						t.Fatal(err)
					}
					beforeRecords, err := json.Marshal(db.UploadingList)
					if err != nil {
						t.Fatal(err)
					}
					remote := pcserror.NewPanErrorInfo("fixture upload")
					remote.ErrNo = tc.code
					remote.SetRemoteError()
					if tc.code == 0 {
						cause := errors.New("synthetic network error")
						if tc.name == "http413" {
							cause = errors.New("413 Request Entity Too Large")
						}
						if tc.name == "source-read" {
							cause = rio.ErrSourceChanged
						}
						remote.SetNetError(cause)
					}
					var transferErr error = remote
					if tc.name == "source-and-skip" {
						transferErr = errors.Join(transferErr, rio.ErrSourceChanged)
					}
					if phase == "part" {
						transferErr = &uploader.MultiError{Err: transferErr, Terminated: true}
					}
					transferErr = fmt.Errorf("upload response context: %w", transferErr)
					if wrapping == "single-join" {
						transferErr = errors.Join(transferErr)
					}
					independent := errors.New("independent upload failure")
					if joined {
						transferErr = errors.Join(transferErr, independent)
					}
					backend := &wrappedPolicyBackend{phase: phase, err: transferErr, digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}}
					u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(local.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
					u.SetInstanceState(state)
					task := &UploadTaskUnit{Context: context.Background(), LocalFileChecksum: local, UploadingDatabase: db, Policy: tc.policy, SavePath: "/fixture", taskInfo: &taskframework.TaskInfo{}, state: state, UploadStatistic: &UploadStatistic{}}
					result := task.executeUploader(u)
					wantSkip := tc.skip && !joined
					if result.Succeed != wantSkip || (result.Extra == baidupcs.SkipPolicy) != wantSkip || result.NeedRetry != tc.retry {
						t.Errorf("wrapped error changed policy: %+v", result)
					}
					if wantSkip {
						if result.Err != nil {
							t.Errorf("normal skip retained an error: %v", result.Err)
						}
					} else if !errors.Is(result.Err, transferErr) || !errors.Is(result.Err, remote) || joined && !errors.Is(result.Err, independent) {
						t.Errorf("original error chain lost: %v", result.Err)
					}
					wantParts, wantMerges := 2, 1
					if phase == "part" {
						wantParts, wantMerges = 1, 0
					}
					if backend.partCalls != wantParts || backend.mergeCalls != wantMerges || task.UploadStatistic.TotalSize() != 0 {
						t.Errorf("terminal failure retried, merged or counted bytes: parts=%d merges=%d bytes=%d", backend.partCalls, backend.mergeCalls, task.UploadStatistic.TotalSize())
					}
					if tc.name == "source-read" || tc.name == "source-and-skip" {
						if !errors.Is(result.Err, rio.ErrSourceChanged) {
							t.Errorf("source failure lost priority: %v", result.Err)
						}
						reloaded, err := NewUploadingDatabaseAt(checkpoint)
						if err != nil {
							t.Fatal(err)
						}
						defer reloaded.Close()
						saved := reloaded.Search(&local.LocalFileMeta)
						if saved == nil || saved.Uploadid != "keep-session" || len(saved.BlockList) != 2 || len(reloaded.UploadingList) != 2 {
							t.Fatalf("source failure lost resumable progress: %+v", saved)
						}
						for _, block := range saved.BlockList {
							if (block.CheckSum != "") != (phase == "merge") {
								t.Errorf("source failure recorded incorrect part completion: %+v", block)
							}
						}
					} else if tc.name == "expired" {
						reloaded, err := NewUploadingDatabaseAt(checkpoint)
						if err != nil {
							t.Fatal(err)
						}
						defer reloaded.Close()
						if reloaded.Search(&local.LocalFileMeta) != nil || len(reloaded.UploadingList) != 1 || reloaded.UploadingList[0].State.Uploadid != "other-session" {
							t.Error("expired session was not removed while retaining unrelated records")
						}
					} else {
						if got, err := os.ReadFile(checkpoint); err != nil || string(got) != string(beforeDisk) {
							t.Fatalf("error changed checkpoint: %q %v", got, err)
						}
						records, err := json.Marshal(db.UploadingList)
						if err != nil || string(records) != string(beforeRecords) {
							t.Fatalf("error changed saved records: %s %v", records, err)
						}
					}
					if got, err := os.ReadFile(source); err != nil || string(got) != "abcdefgh" {
						t.Fatalf("error changed source: %q %v", got, err)
					}
				})
			}
		}
	}
}

func TestWrappedExpiredUploadRetainsCheckpointSaveFailure(t *testing.T) {
	for _, phase := range []string{"part", "merge"} {
		for _, joined := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/join=%t", phase, joined), func(t *testing.T) {
				task, database, original, cleanup := checkpointFailureTask(t)
				defer cleanup()
				task.UploadingDatabase.openFile = os.OpenFile
				task.UploadingDatabase.UpdateUploading(&task.LocalFileChecksum.LocalFileMeta, task.state)
				if err := task.UploadingDatabase.Save(); err != nil {
					t.Fatal(err)
				}
				var err error
				original, err = os.ReadFile(database)
				if err != nil {
					t.Fatal(err)
				}
				task.UploadingDatabase.openFile = func(path string, _ int, _ os.FileMode) (*os.File, error) { return os.Open(path) }
				remote := pcserror.NewPanErrorInfo("fixture expired session")
				remote.ErrNo = 31363
				remote.SetRemoteError()
				var transferErr error = remote
				if phase == "part" {
					transferErr = &uploader.MultiError{Err: transferErr, Terminated: true}
				}
				transferErr = fmt.Errorf("expired upload response: %w", transferErr)
				if joined {
					transferErr = errors.Join(transferErr, errors.New("independent failure"))
				}
				backend := &wrappedPolicyBackend{phase: phase, err: transferErr, digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}}
				u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
				u.SetInstanceState(task.state)
				reports := 0
				task.OnTransferError = func(err error) {
					reports++
					if !errors.Is(err, ErrCheckpointSave) {
						t.Errorf("checkpoint observer lost save error: %v", err)
					}
				}
				result := task.executeUploader(u)
				var pathErr *os.PathError
				if result.Succeed || result.NeedRetry || result.ResultMessage != "清理上传进度失败" || !errors.Is(result.Err, transferErr) || !errors.Is(result.Err, remote) || !errors.Is(result.Err, ErrCheckpointSave) || !errors.As(result.Err, &pathErr) || reports != 1 {
					t.Errorf("expired-session cleanup lost causes: %+v reports=%d", result, reports)
				}
				if got, err := os.ReadFile(database); err != nil || string(got) != string(original) {
					t.Fatalf("failed cleanup changed old checkpoint: %q %v", got, err)
				}
				reloaded, err := NewUploadingDatabaseAt(database)
				if err != nil {
					t.Fatal(err)
				}
				defer reloaded.Close()
				if state := reloaded.Search(&task.LocalFileChecksum.LocalFileMeta); state == nil || state.Uploadid != task.state.Uploadid {
					t.Fatalf("failed cleanup lost the on-disk session: %+v", state)
				}
				if got, err := os.ReadFile(task.LocalFileChecksum.Path); err != nil || string(got) != "abcdefgh" {
					t.Fatalf("failed cleanup changed source: %q %v", got, err)
				}
			})
		}
	}
}

func TestUploadPolicyErrorRetainsInnerFailure(t *testing.T) {
	for _, phase := range []string{"part", "merge"} {
		for _, tc := range []struct {
			policy string
			code   int
		}{{baidupcs.SkipPolicy, 114514}, {baidupcs.RsyncPolicy, 1919810}, {baidupcs.SkipPolicy, 31061}} {
			t.Run(fmt.Sprintf("%s/%d", phase, tc.code), func(t *testing.T) {
				task, database, original, cleanup := checkpointFailureTask(t)
				defer cleanup()
				task.UploadingDatabase.openFile = os.OpenFile
				task.Policy = tc.policy
				independent := errors.New("independent inner upload failure")
				remote := pcserror.NewPanErrorInfo("fixture upload")
				remote.ErrNo = tc.code
				remote.SetRemoteError()
				remote.Err = fmt.Errorf("inner context: %w", independent)
				var transferErr error = remote
				if phase == "part" {
					transferErr = &uploader.MultiError{Err: transferErr, Terminated: true}
				}
				backend := &wrappedPolicyBackend{phase: phase, err: transferErr, digestResumeBackend: digestResumeBackend{parts: make(map[int]string)}}
				u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(task.LocalFileChecksum.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
				u.SetInstanceState(task.state)
				result := task.executeUploader(u)
				if result.Succeed || result.NeedRetry || result.Extra == baidupcs.SkipPolicy || !errors.Is(result.Err, independent) || !errors.Is(result.Err, remote) {
					t.Fatalf("policy result hid inner failure: %+v", result)
				}
				if data, err := os.ReadFile(database); err != nil || string(data) != string(original) {
					t.Fatalf("failure changed checkpoint: %q %v", data, err)
				}
				if data, err := os.ReadFile(task.LocalFileChecksum.Path); err != nil || string(data) != "abcdefgh" {
					t.Fatalf("failure changed source: %q %v", data, err)
				}
				if task.UploadStatistic.TotalSize() != 0 {
					t.Fatal("failure counted uploaded bytes")
				}
			})
		}
	}
}
