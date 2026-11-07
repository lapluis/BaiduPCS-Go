package pcsupload

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func resumeVerificationFixture(t *testing.T, cachedDigest bool) (*UploadTaskUnit, string, []byte) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(1700000000, 123456700)
	if err := os.Chtimes(source, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	old := checksum.NewLocalFileChecksum(source, 4)
	if err := old.OpenPath(); err != nil {
		t.Fatal(err)
	}
	if cachedDigest {
		if err := old.Sum(checksum.CHECKSUM_MD5 | checksum.CHECKSUM_SLICE_MD5); err != nil {
			t.Fatal(err)
		}
		if err := old.CalculateChunkedSum(4); err != nil {
			t.Fatal(err)
		}
	}
	defer old.Close()
	database := source + ".state"
	db, err := NewUploadingDatabaseAt(database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	digest := md5.Sum([]byte("abcd"))
	db.UpdateUploading(&old.LocalFileMeta, &uploader.InstanceState{Uploadid: "saved-session", BlockList: []*uploader.BlockState{
		{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: hex.EncodeToString(digest[:])},
		{ID: 1, Range: transfer.Range{Begin: 4, End: 8}},
	}})
	if err := db.Save(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(database)
	if err != nil {
		t.Fatal(err)
	}
	current := checksum.NewLocalFileChecksum(source, 4)
	if err := current.OpenPath(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { current.Close() })
	return &UploadTaskUnit{LocalFileChecksum: current, UploadingDatabase: db}, database, before
}

func TestResumeVerificationChecksCurrentBytes(t *testing.T) {
	for _, cachedDigest := range []bool{false, true} {
		for _, providedDigest := range []bool{false, true} {
			for _, changed := range []bool{false, true} {
				t.Run(fmt.Sprintf("cached=%t/provided=%t/changed=%t", cachedDigest, providedDigest, changed), func(t *testing.T) {
					task, database, before := resumeVerificationFixture(t, cachedDigest)
					local := task.LocalFileChecksum
					content := []byte("abcdefgh")
					if changed {
						content[0] = 'z'
						if err := os.WriteFile(local.Path, content, 0600); err != nil {
							t.Fatal(err)
						}
						stamp := time.Unix(0, local.ModTimeNS)
						if err := os.Chtimes(local.Path, stamp, stamp); err != nil {
							t.Fatal(err)
						}
					}
					if providedDigest {
						if err := local.Sum(checksum.CHECKSUM_MD5 | checksum.CHECKSUM_SLICE_MD5); err != nil {
							t.Fatal(err)
						}
					}
					state, err := task.verifiedResumeState(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					if changed && cachedDigest {
						if state != nil || local.BlocksList != nil {
							t.Fatal("changed version retained old session or chunk hashes")
						}
					} else {
						if state == nil || state.Uploadid != "saved-session" || len(state.BlockList) != 2 {
							t.Fatal("usable session lost")
						}
						digest := md5.Sum(content[:4])
						want := hex.EncodeToString(digest[:])
						if changed {
							want = ""
						}
						if state.BlockList[0].CheckSum != want || state.BlockList[1].CheckSum != "" {
							t.Fatal("completed part reused after source rewrite")
						}
					}
					if cachedDigest || providedDigest {
						want := md5.Sum(content)
						if !bytes.Equal(local.MD5, want[:]) {
							t.Fatal("cached whole-file digest survived changed source")
						}
					} else if local.MD5 != nil {
						t.Fatal("no-rapid verification unexpectedly computed whole-file digest")
					}
					if data, err := os.ReadFile(database); err != nil || !bytes.Equal(data, before) {
						t.Fatal("verification prematurely replaced checkpoint", err)
					}
				})
			}
		}
	}
}

func TestFailedResumeVerificationPreservesCheckpoint(t *testing.T) {
	for _, cachedDigest := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("cached=%t/canceled=%t", cachedDigest, canceled), func(t *testing.T) {
				task, database, before := resumeVerificationFixture(t, cachedDigest)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if canceled {
					cancel()
				} else if err := task.LocalFileChecksum.GetFile().Close(); err != nil {
					t.Fatal(err)
				}
				state, err := task.verifiedResumeState(ctx)
				if err == nil || state != nil || len(task.UploadingDatabase.UploadingList) != 1 {
					t.Fatal("failed verification discarded or published state", err)
				}
				if data, err := os.ReadFile(database); err != nil || !bytes.Equal(data, before) {
					t.Fatal("failed verification changed persisted progress", err)
				}
			})
		}
	}
}

func TestNoRapidResumeReuploadsOnlyChangedCompletedPart(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(fmt.Sprintf("changed=%t", changed), func(t *testing.T) {
			task, _, _ := resumeVerificationFixture(t, false)
			local := task.LocalFileChecksum
			state := task.UploadingDatabase.Search(&local.LocalFileMeta)
			second := md5.Sum([]byte("efgh"))
			state.BlockList[1].CheckSum = hex.EncodeToString(second[:])
			task.UploadingDatabase.UpdateUploading(&local.LocalFileMeta, state)
			if err := task.UploadingDatabase.Save(); err != nil {
				t.Fatal(err)
			}
			first := "abcd"
			if changed {
				first = "zbcd"
				if err := os.WriteFile(local.Path, []byte(first+"efgh"), 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(0, local.ModTimeNS)
				if err := os.Chtimes(local.Path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			state, err := task.verifiedResumeState(context.Background())
			if err != nil || state == nil {
				t.Fatal("could not restore no-rapid session", err)
			}
			backend := &digestResumeBackend{parts: make(map[int]string), wantParts: []string{first, "efgh"}}
			u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(local.GetFile()), &uploader.MultiUploaderConfig{
				Parallel: 1, BlockSize: 4, ValidateSource: local.ValidateSnapshot,
			}, "/fixture")
			u.SetInstanceState(state)
			success := false
			u.OnSuccess(func() { success = true })
			if err := u.ExecuteWithError(); err != nil || !success || !backend.merged {
				t.Fatal("verified resume did not merge current source", err)
			}
			wantCount := 0
			if changed {
				wantCount = 1
			}
			if len(backend.parts) != wantCount || (changed && backend.parts[0] != first) {
				t.Fatalf("unchanged completed part was resent or changed part was reused: %v", backend.parts)
			}
			if local.MD5 != nil || local.BlocksList != nil {
				t.Fatal("no-rapid resume unexpectedly computed whole-file metadata")
			}
		})
	}
}

type resumeReadFunc func([]byte) (int, error)

func (f resumeReadFunc) Read(p []byte) (int, error) { return f(p) }

func TestUploadResumeReaderKeepsCancellationAndReadFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failure := errors.New("fixture source read failure")
	calls := 0
	reader := &uploadResumeReader{ctx: ctx, reader: resumeReadFunc(func([]byte) (int, error) {
		calls++
		cancel()
		return 0, failure
	})}
	if _, err := io.ReadAll(reader); !errors.Is(err, failure) || !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("resume read lost cause or continued after cancellation", err, calls)
	}
	if _, err := reader.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("canceled resume reader touched the source again", err, calls)
	}
}
