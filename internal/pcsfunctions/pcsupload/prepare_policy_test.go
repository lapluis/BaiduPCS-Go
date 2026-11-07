package pcsupload

import (
	"context"
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
)

func TestNoRapidPreparationRequiresMatchingSkipPolicy(t *testing.T) {
	for _, tc := range []struct {
		policy           string
		code             int
		local, malformed bool
	}{
		{baidupcs.OverWritePolicy, 114514, false, false},
		{baidupcs.RsyncPolicy, 114514, false, false},
		{baidupcs.OverWritePolicy, 1919810, false, false},
		{baidupcs.SkipPolicy, 1919810, false, false},
		{baidupcs.SkipPolicy, 114514, true, false},
		{baidupcs.RsyncPolicy, 1919810, true, false},
		{baidupcs.SkipPolicy, 114514, false, true},
		{baidupcs.RsyncPolicy, 1919810, false, true},
	} {
		t.Run(fmt.Sprintf("%s/%d/malformed=%t", tc.policy, tc.code, tc.malformed), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(source, []byte("abcdefgh"), 0600); err != nil {
				t.Fatal(err)
			}
			local := checksum.NewLocalFileChecksum(source, 4)
			if err := local.OpenPath(); err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			db, err := NewUploadingDatabaseAt(source + ".state")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			mutations := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
				payload := fmt.Sprintf(`{"error_code":%d}`, tc.code)
				switch {
				case r.URL.Query().Get("method") == "meta":
					if tc.local {
						payload = `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","size":8}]}`
					} else if tc.malformed {
						payload = fmt.Sprintf(`{"error_code":%d,"list":"invalid"}`, tc.code)
					}
				case r.URL.Path == "/api/list":
					payload = `{"errno":0,"list":[]}`
				case r.URL.Path == "/api/user/getinfo":
					payload = `{"errno":0,"records":[{"uk":7}]}`
				default:
					mutations++
					t.Error("unexpected mutation", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
			})
			task := &UploadTaskUnit{Context: context.Background(), LocalFileChecksum: local, UploadingDatabase: db, PCS: baidupcs.NewPCSWithClient(1, client), SavePath: "/fixture", Policy: tc.policy, NoRapidUpload: true, taskInfo: &taskframework.TaskInfo{}, UploadStatistic: &UploadStatistic{}}
			task.prepareFile()
			if tc.local {
				if task.Step != JustGoon || task.prepareResult == nil || !task.prepareResult.Succeed || task.prepareResult.Extra != baidupcs.SkipPolicy {
					t.Fatalf("legitimate policy skip changed: step=%v result=%+v", task.Step, task.prepareResult)
				}
			} else {
				if task.Step != StepUploadRapidUpload || task.prepareResult != nil {
					t.Fatalf("wrong policy marked skipped: step=%v result=%+v", task.Step, task.prepareResult)
				}
				next, result := task.rapidUpload()
				var remote pcserror.Error
				kind := pcserror.ErrTypeRemoteError
				if tc.malformed {
					kind = pcserror.ErrTypeJSONParseError
				}
				if next || result.Succeed || result.Extra == baidupcs.SkipPolicy || !errors.As(result.Err, &remote) || remote.GetErrType() != kind || remote.GetRemoteErrCode() != tc.code {
					t.Fatalf("fallback hid policy mismatch: next=%t result=%+v", next, result)
				}
			}
			if mutations != 0 || task.UploadStatistic.TotalSize() != 0 || db.Search(&local.LocalFileMeta) != nil {
				t.Fatal("skip/failure mutated upload state or counted bytes")
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != "abcdefgh" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		})
	}
}
