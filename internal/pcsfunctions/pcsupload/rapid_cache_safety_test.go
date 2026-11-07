package pcsupload

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestRapidCachedMatchRequiresCurrentSourceAndCompleteMetadata(t *testing.T) {
	for _, mode := range []string{"matching", "uppercase", "malformed-remote", "remote-size", "remote-directory", "short-local-md5", "empty-local-slice", "skip-policy", "ignore-match", "changed-during-list", "grown-during-list", "shrunk-during-list", "closed-during-list", "changed-during-uk", "changed-during-precreate", "changed-during-precreate-error", "rapid-hit"} {
		t.Run(mode, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source")
			original := []byte("abcdefgh")
			if err := os.WriteFile(source, original, 0600); err != nil {
				t.Fatal(err)
			}
			local := checksum.NewLocalFileChecksum(source, int(baidupcs.SliceMD5Size))
			if err := local.OpenPath(); err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			if err := local.Sum(checksum.CHECKSUM_MD5 | checksum.CHECKSUM_SLICE_MD5); err != nil {
				t.Fatal(err)
			}
			if err := local.CalculateChunkedSum(getBlockSize(local.Length)); err != nil {
				t.Fatal(err)
			}
			digest := hex.EncodeToString(local.MD5)
			checkpoint := source + ".saved-upload"
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
			storedCheckpoint, err := os.ReadFile(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			storedRecords, err := json.Marshal(db.UploadingList)
			if err != nil {
				t.Fatal(err)
			}
			remoteSize, directory := int64(len(original)), 0
			if mode == "uppercase" {
				digest = strings.ToUpper(digest)
			}
			if mode == "malformed-remote" {
				digest += "zz"
			}
			if mode == "remote-size" {
				remoteSize++
			}
			if mode == "remote-directory" {
				directory = 1
			}
			if mode == "short-local-md5" {
				local.MD5 = []byte("bad")
			}
			if mode == "empty-local-slice" {
				local.SliceMD5 = []byte{}
			}
			if mode == "skip-policy" {
				digest = "unusable hash"
				remoteSize++
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			requests := 0
			wantedContent := original
			var inspectionCause error
			precreateFlow := strings.HasPrefix(mode, "changed-during-precreate") || mode == "rapid-hit"
			rapidFlow := precreateFlow || mode == "changed-during-uk"
			client := requester.NewHTTPClient()
			client.Client.Transport = uploadRoundTrip(func(req *http.Request) (*http.Response, error) {
				requests++
				phase := ""
				if req.URL.Path == "/api/list" {
					phase = "list"
				}
				if req.URL.Path == "/api/user/getinfo" {
					phase = "uk"
				}
				if req.URL.Query().Get("method") == "meta" {
					phase = "meta"
				}
				if req.URL.Path == "/api/precreate" {
					phase = "precreate"
				}
				if phase == "list" && mode == "closed-during-list" {
					if err := local.GetFile().Close(); err != nil {
						t.Fatal(err)
					}
					_, inspectionCause = local.GetFile().Stat()
					if inspectionCause == nil {
						t.Fatal("closed handle unexpectedly passed inspection")
					}
					var pathErr *os.PathError
					if errors.As(inspectionCause, &pathErr) {
						inspectionCause = pathErr.Err
					}
				} else if phase == "list" && strings.Contains(mode, "during-list") || phase == "uk" && mode == "changed-during-uk" || phase == "precreate" && strings.HasPrefix(mode, "changed-during-precreate") {
					wantedContent = []byte("abcdEFGH")
					if mode == "grown-during-list" {
						wantedContent = []byte("abcdefghi")
					}
					if mode == "shrunk-during-list" {
						wantedContent = []byte("abcdefg")
					}
					if err := os.WriteFile(source, wantedContent, 0600); err != nil {
						t.Fatal(err)
					}
					stamp := time.Unix(local.ModTime+1, 0)
					if err := os.Chtimes(source, stamp, stamp); err != nil {
						t.Fatal(err)
					}
				}
				payload := ""
				switch phase {
				case "list":
					entry := map[string]interface{}{"fs_id": 1, "path": "/fixture", "server_filename": "fixture", "size": remoteSize, "isdir": directory, "md5": digest}
					entries := []map[string]interface{}{entry}
					if rapidFlow {
						entries = []map[string]interface{}{}
					}
					data, err := json.Marshal(map[string]interface{}{"errno": 0, "list": entries})
					if err != nil {
						t.Fatal(err)
					}
					payload = string(data)
				case "uk":
					if rapidFlow {
						payload = `{"errno":0,"records":[{"uk":7}]}`
						break
					}
					fallthrough
				default:
					if phase == "meta" && precreateFlow {
						payload = `{"error_code":31066}`
						break
					}
					if phase == "precreate" && precreateFlow {
						payload = `{"errno":0,"return_type":2}`
						if mode == "changed-during-precreate-error" {
							payload = `{"errno":31112}`
						}
						break
					}
					cancel() // Stop non-matching cases before any remote write.
					return nil, ctx.Err()
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			task := &UploadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, UploadingDatabase: db, SavePath: "/fixture", panDir: "/", panFile: "fixture", Policy: baidupcs.OverWritePolicy, state: state, taskInfo: &taskframework.TaskInfo{}, UploadStatistic: &UploadStatistic{}}
			if mode == "skip-policy" {
				task.Policy = baidupcs.SkipPolicy
			}
			task.IgnoreCachedMatch = mode == "ignore-match"
			next, result := task.rapidUpload()
			changed := strings.Contains(mode, "during-")
			wantSuccess := mode == "matching" || mode == "uppercase" || mode == "short-local-md5" || mode == "empty-local-slice" || mode == "skip-policy" || mode == "rapid-hit"
			if next || result.Succeed != wantSuccess || result.NeedRetry {
				t.Fatalf("unsafe cached match: next=%v requests=%d result=%+v", next, requests, result)
			}
			if changed {
				wantedRequests := 1
				if mode == "changed-during-uk" {
					wantedRequests = 2
				}
				if strings.HasPrefix(mode, "changed-during-precreate") {
					wantedRequests = 4
				}
				if !errors.Is(result.Err, rio.ErrSourceChanged) || errors.Is(result.Err, context.Canceled) || requests != wantedRequests {
					t.Fatalf("changed source escaped validation: requests=%d result=%+v", requests, result)
				}
				if mode == "closed-during-list" && !errors.Is(result.Err, inspectionCause) {
					t.Fatalf("source inspection cause lost: %v", result.Err)
				}
				if mode == "changed-during-precreate-error" {
					var remote pcserror.Error
					if !errors.As(result.Err, &remote) || remote.GetRemoteErrCode() != 31112 {
						t.Fatalf("source change hid original server error: %v", result.Err)
					}
				}
			} else if wantSuccess {
				wantedRequests := 1
				if mode == "rapid-hit" {
					wantedRequests = 4
				}
				if result.Err != nil || requests != wantedRequests || len(local.MD5) != md5.Size || len(local.SliceMD5) != md5.Size {
					t.Fatalf("healthy cache match failed or retained incomplete digests: requests=%d result=%+v md5=%x slice=%x", requests, result, local.MD5, local.SliceMD5)
				}
			} else if !errors.Is(result.Err, context.Canceled) || requests != 2 {
				t.Fatalf("invalid metadata did not continue to preparation: requests=%d result=%+v", requests, result)
			}
			wantedBytes := int64(0)
			if mode == "rapid-hit" {
				wantedBytes = int64(len(original))
			}
			if task.UploadStatistic.TotalSize() != wantedBytes {
				t.Fatalf("source change counted as uploaded: %d", task.UploadStatistic.TotalSize())
			}
			if !wantSuccess {
				if task.state.Uploadid != "keep-session" {
					t.Fatal("failed validation replaced the resume session")
				}
				if got, err := os.ReadFile(checkpoint); err != nil || string(got) != string(storedCheckpoint) {
					t.Fatalf("failed validation changed checkpoint: %q %v", got, err)
				}
				records, err := json.Marshal(db.UploadingList)
				if err != nil || string(records) != string(storedRecords) {
					t.Fatalf("failed validation changed in-memory resume records: %s %v", records, err)
				}
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != string(wantedContent) {
				t.Fatalf("validation changed source: %q %v", got, err)
			}
		})
	}
}
