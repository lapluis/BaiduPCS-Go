package pcsupload

import (
	"context"
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

func TestNoRapidPreparationValidatesSourceBeforeAcceptingSession(t *testing.T) {
	for _, mode := range []string{"healthy", "resume", "skip", "stale-before-lookup", "closed-before-lookup", "quota-change", "meta-change", "precreate-change", "precreate-error-change", "precreate-error", "skip-meta-change"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			source, checkpoint := filepath.Join(root, "source"), filepath.Join(root, "state.json")
			wantedContent := []byte("abcdefgh")
			if err := os.WriteFile(source, wantedContent, 0600); err != nil {
				t.Fatal(err)
			}
			if mode == "quota-change" {
				if err := os.Truncate(source, baidupcs.MinCheckLeftSpaceThreshold+1); err != nil {
					t.Fatal(err)
				}
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
			if mode == "resume" || strings.HasSuffix(mode, "before-lookup") {
				db.UpdateUploading(&local.LocalFileMeta, state)
			} else {
				db.UpdateUploading(&checksum.LocalFileMeta{Path: filepath.Join(root, "other"), Length: 1, ModTime: -1}, &uploader.InstanceState{Uploadid: "other-session"})
			}
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
			mutateSource := func() {
				wantedContent = []byte("abcdEFGH")
				if err := os.WriteFile(source, wantedContent, 0600); err != nil {
					t.Fatal(err)
				}
				stamp := time.Unix(local.ModTime+1, 0)
				if err := os.Chtimes(source, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			var inspectionCause error
			if mode == "stale-before-lookup" {
				mutateSource()
			}
			if mode == "closed-before-lookup" {
				if err := local.GetFile().Close(); err != nil {
					t.Fatal(err)
				}
				_, inspectionCause = local.GetFile().Stat()
				var pathErr *os.PathError
				if errors.As(inspectionCause, &pathErr) {
					inspectionCause = pathErr.Err
				}
			}
			requests := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = uploadRoundTrip(func(req *http.Request) (*http.Response, error) {
				requests++
				phase := "precreate"
				if req.URL.Query().Get("method") == "meta" {
					phase = "meta"
				}
				if strings.HasSuffix(req.URL.Path, "/quota") {
					phase = "quota"
				}
				if phase == "quota" && mode == "quota-change" || phase == "meta" && (mode == "meta-change" || mode == "skip-meta-change") || phase == "precreate" && (mode == "precreate-change" || mode == "precreate-error-change") {
					mutateSource()
				}
				payload := `{"errno":0,"return_type":1,"uploadid":"new-session"}`
				if phase == "quota" {
					payload = `{"quota":1099511627776,"used":0}`
				}
				if phase == "meta" {
					payload = `{"error_code":31066}`
					if mode == "skip" || mode == "skip-meta-change" {
						payload = `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","server_filename":"fixture","size":8,"isdir":0}]}`
					}
				}
				if phase == "precreate" && strings.HasPrefix(mode, "precreate-error") {
					payload = `{"errno":31112}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			task := &UploadTaskUnit{Context: context.Background(), PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, UploadingDatabase: db, NoRapidUpload: true, SavePath: "/fixture", Policy: baidupcs.OverWritePolicy, taskInfo: &taskframework.TaskInfo{}, state: state}
			if mode == "skip" || mode == "skip-meta-change" {
				task.Policy = baidupcs.SkipPolicy
			}
			task.prepareFile()
			failedSource := strings.HasSuffix(mode, "-change") || strings.HasSuffix(mode, "before-lookup")
			wantedRequests := 2
			if mode == "resume" || strings.HasSuffix(mode, "before-lookup") {
				wantedRequests = 0
			}
			if mode == "quota-change" || mode == "skip" || mode == "skip-meta-change" {
				wantedRequests = 1
			}
			if requests != wantedRequests {
				t.Errorf("preparation sent unexpected requests: got=%d want=%d", requests, wantedRequests)
			}
			if failedSource {
				result := task.prepareResult
				if task.Step != JustGoon || result == nil || result.Succeed || result.NeedRetry || !errors.Is(result.Err, rio.ErrSourceChanged) {
					t.Fatalf("source failure accepted or fell back: step=%v result=%+v", task.Step, result)
				}
				if task.state != nil && task.state.Uploadid == "new-session" {
					t.Fatal("failed source accepted new session")
				}
				if inspectionCause != nil && !errors.Is(result.Err, inspectionCause) {
					t.Fatalf("source inspection cause lost: %v", result.Err)
				}
				if mode == "precreate-error-change" {
					var remote pcserror.Error
					if !errors.As(result.Err, &remote) || remote.GetRemoteErrCode() != 31112 {
						t.Fatalf("source failure hid remote error: %v", result.Err)
					}
				}
			} else {
				switch mode {
				case "skip":
					if task.Step != JustGoon || task.prepareResult == nil || !task.prepareResult.Succeed || task.prepareResult.Extra != baidupcs.SkipPolicy {
						t.Fatalf("healthy skip changed: %+v", task.prepareResult)
					}
				case "precreate-error":
					if task.Step != StepUploadRapidUpload {
						t.Fatalf("healthy-source fallback changed: %v", task.Step)
					}
				default:
					wantSession := "new-session"
					if mode == "resume" {
						wantSession = "keep-session"
					}
					if task.Step != StepUploadUpload || task.state == nil || task.state.Uploadid != wantSession || task.prepareResult != nil {
						t.Fatalf("healthy preparation changed: step=%v state=%+v result=%+v", task.Step, task.state, task.prepareResult)
					}
				}
			}
			if mode != "healthy" {
				if got, err := os.ReadFile(checkpoint); err != nil || string(got) != string(beforeDisk) {
					t.Fatalf("failed/skipped preparation changed checkpoint: %q %v", got, err)
				}
				records, err := json.Marshal(db.UploadingList)
				if err != nil || string(records) != string(beforeRecords) {
					t.Fatalf("preparation changed existing in-memory records: %s %v", records, err)
				}
			} else {
				reloaded, err := NewUploadingDatabaseAt(checkpoint)
				if err != nil {
					t.Fatal(err)
				}
				defer reloaded.Close()
				found := reloaded.Search(&local.LocalFileMeta)
				if found == nil || found.Uploadid != "new-session" || len(reloaded.UploadingList) != 2 {
					t.Fatalf("healthy session was not saved with existing records: %+v", found)
				}
			}
			if local.MD5 != nil || local.SliceMD5 != nil || local.BlocksList != nil {
				t.Fatal("no-rapid preparation unexpectedly computed checksums")
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != string(wantedContent) {
				t.Fatalf("preparation changed source: %q %v", got, err)
			}
		})
	}
}
