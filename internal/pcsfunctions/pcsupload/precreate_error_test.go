package pcsupload

import (
	"context"
	"errors"
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
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

func TestRapidPreparationRetainsPrecreateFailure(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		kind           pcserror.ErrType
	}{
		{"invalid-session", `{"errno":0,"return_type":1}`, pcserror.ErrTypeInternalError},
		{"remote-quota", `{"errno":31112}`, pcserror.ErrTypeRemoteError},
		{"malformed-json", `{"errno":`, pcserror.ErrTypeJSONParseError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "file")
			if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			local := checksum.NewLocalFileChecksum(source, 4)
			if err := local.OpenPath(); err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			client := requester.NewHTTPClient()
			client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
				data := tc.response
				switch {
				case r.URL.Path == "/api/list":
					data = `{"errno":0,"list":[]}`
				case r.URL.Path == "/api/user/getinfo":
					data = `{"errno":0,"records":[{"uk":7}]}`
				case r.URL.Query().Get("method") == "meta":
					data = `{"error_code":31066}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(data))}, nil
			})
			task := &UploadTaskUnit{Context: context.Background(), PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, SavePath: "/fixture", panDir: "/", panFile: "fixture", Policy: baidupcs.OverWritePolicy, state: &uploader.InstanceState{}, taskInfo: &taskframework.TaskInfo{}}
			next, result := task.rapidUpload()
			var pcsErr pcserror.Error
			if next || result.Succeed || !errors.As(result.Err, &pcsErr) || pcsErr.GetErrType() != tc.kind || task.state.Uploadid != "" {
				t.Fatalf("precreate failure lost: next=%v result=%+v state=%+v", next, result, task.state)
			}
			if tc.name == "invalid-session" && !errors.Is(pcsErr.GetError(), baidupcs.ErrUploadPrecreateInvalid) {
				t.Fatalf("invalid session failure cause lost: %v", pcsErr)
			}
		})
	}
}
