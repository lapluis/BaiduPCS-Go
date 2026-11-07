package pcsupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type policyErrorBackend struct {
	digestResumeBackend
	err pcserror.Error
}

func (b *policyErrorBackend) Precreate(context.Context) (string, pcserror.Error) {
	return "", b.err
}

func TestUploadPolicySkipHasSuccessfulResult(t *testing.T) {
	for _, phase := range []string{"rapid", "parts"} {
		for _, tc := range []struct {
			name, policy string
			code         int
			skipped      bool
		}{
			{"existing-skip", baidupcs.SkipPolicy, 114514, true},
			{"same-size-rsync", baidupcs.RsyncPolicy, 1919810, true},
			{"conflict-skip", baidupcs.SkipPolicy, 31061, phase == "parts"},
			{"conflict-overwrite", baidupcs.OverWritePolicy, 31061, false},
			{"sentinel-overwrite", baidupcs.OverWritePolicy, 114514, false},
			{"skip-sentinel-rsync", baidupcs.RsyncPolicy, 114514, false},
			{"rsync-sentinel-overwrite", baidupcs.OverWritePolicy, 1919810, false},
			{"rsync-sentinel-skip", baidupcs.SkipPolicy, 1919810, false},
			{"quota-skip", baidupcs.SkipPolicy, 31112, false},
		} {
			t.Run(phase+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				source, checkpoint := filepath.Join(root, "source"), filepath.Join(root, "state.json")
				if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
					t.Fatal(err)
				}
				local := checksum.NewLocalFileChecksum(source, int(baidupcs.SliceMD5Size))
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
				requests := 0
				client := requester.NewHTTPClient()
				client.Client.Transport = uploadRoundTrip(func(req *http.Request) (*http.Response, error) {
					requests++
					payload := fmt.Sprintf(`{"errno":%d}`, tc.code)
					switch {
					case req.URL.Path == "/api/list":
						payload = `{"errno":0,"list":[]}`
					case req.URL.Path == "/api/user/getinfo":
						payload = `{"errno":0,"records":[{"uk":7}]}`
					case req.URL.Query().Get("method") == "meta":
						payload = `{"error_code":31066}`
						if tc.name == "existing-skip" || tc.name == "same-size-rsync" {
							payload = `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","server_filename":"fixture","size":8,"isdir":0}]}`
						}
					case req.URL.Path != "/api/precreate":
						t.Errorf("unexpected request: %s", req.URL.Path)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req, Body: io.NopCloser(strings.NewReader(payload))}, nil
				})
				task := &UploadTaskUnit{Context: context.Background(), PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, UploadingDatabase: db, Policy: tc.policy, SavePath: "/fixture", panDir: "/", panFile: "fixture", taskInfo: &taskframework.TaskInfo{}, state: state, UploadStatistic: &UploadStatistic{}}
				var result *taskframework.TaskUnitRunResult
				if phase == "rapid" {
					next, outcome := task.rapidUpload()
					result = outcome
					if next {
						t.Fatal("policy result continued to part upload")
					}
					wantRequests := 4
					if tc.name == "existing-skip" || tc.name == "same-size-rsync" {
						wantRequests = 3
					}
					if requests != wantRequests {
						t.Errorf("unexpected handshake count: got=%d want=%d", requests, wantRequests)
					}
				} else {
					remote := pcserror.NewPanErrorInfo(baidupcs.OperationUploadPrecreate)
					remote.ErrNo = tc.code
					remote.SetRemoteError()
					backend := &policyErrorBackend{err: remote}
					u := uploader.NewMultiUploader(backend, rio.NewFileReaderAtLen64(local.GetFile()), &uploader.MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
					u.SetInstanceState(state)
					result = task.executeUploader(u)
					if backend.merged || len(backend.parts) != 0 || requests != 0 {
						t.Fatal("preparation error continued transfer")
					}
				}
				if tc.skipped {
					if !result.Succeed || result.NeedRetry || result.Err != nil || result.Extra != baidupcs.SkipPolicy {
						t.Errorf("policy skip reported failure: %+v", result)
					}
				} else {
					var remote pcserror.Error
					if result.Succeed || result.Extra == baidupcs.SkipPolicy || !errors.As(result.Err, &remote) || remote.GetRemoteErrCode() != tc.code {
						t.Errorf("real error was hidden: %+v", result)
					}
				}
				if task.UploadStatistic.TotalSize() != 0 {
					t.Error("skip or failure counted as uploaded bytes")
				}
				if got, err := os.ReadFile(checkpoint); err != nil || string(got) != string(beforeDisk) {
					t.Fatalf("policy result changed checkpoint: %q %v", got, err)
				}
				records, err := json.Marshal(db.UploadingList)
				if err != nil || string(records) != string(beforeRecords) || state.Uploadid != "keep-session" {
					t.Fatalf("policy result changed saved session: %s %v", records, err)
				}
				if got, err := os.ReadFile(source); err != nil || string(got) != "abcdefgh" {
					t.Fatalf("policy result changed source: %q %v", got, err)
				}
			})
		}
	}
}
