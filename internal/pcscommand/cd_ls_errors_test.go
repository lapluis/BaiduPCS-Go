package pcscommand

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestChangeDirectoryErrorAndPersistence(t *testing.T) {
	for _, state := range []string{"metadata-error", "not-directory", "success", "relative", "list-error", "list-success", "save-error", "glob-error", "glob-empty", "glob-many"} {
		t.Run(state, func(t *testing.T) {
			name := changeDirectoryFixture(t)
			before, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if state == "save-error" {
				if err := os.Rename(name, name+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(name, 0700); err != nil {
					t.Fatal(err)
				}
			}
			target, resolved := "/target", "/target"
			if state == "relative" {
				target, resolved = "child", "/old/child"
			}
			if strings.HasPrefix(state, "glob-") {
				target = "/targ*"
			}
			requests := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"errno":0,"list":[]}`
				if req.URL.Query().Get("method") == "meta" {
					if err := req.ParseMultipartForm(4096); err != nil {
						t.Fatal(err)
					}
					if req.MultipartForm != nil {
						defer req.MultipartForm.RemoveAll()
					}
					var params baidupcs.PathsListJSON
					if err := json.Unmarshal([]byte(req.FormValue("param")), &params); err != nil {
						t.Fatal(err)
					}
					if len(params.List) != 1 || params.List[0] == nil || params.List[0].Path != resolved {
						t.Fatalf("wrong metadata path: %+v", params)
					}
					payload = `{"error_code":0,"list":[{"fs_id":1,"path":"` + resolved + `","isdir":1}]}`
					if state == "metadata-error" {
						payload = `{"error_code":31045}`
					}
					if state == "not-directory" {
						payload = `{"error_code":0,"list":[{"fs_id":1,"path":"/target","isdir":0}]}`
					}
				} else if req.URL.Path == "/api/list" {
					wantDir := resolved
					if strings.HasPrefix(state, "glob-") {
						wantDir = "/"
					}
					if req.URL.Query().Get("dir") != wantDir {
						t.Fatalf("wrong listing path: %s", req.URL)
					}
					if state == "list-error" || state == "glob-error" {
						payload = `{"errno":2}`
					}
					if state == "glob-many" {
						payload = `{"errno":0,"list":[{"fs_id":1,"path":"/target","server_filename":"target","isdir":1},{"fs_id":2,"path":"/target2","server_filename":"target2","isdir":1}]}`
					}
				} else {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			list := state == "list-error" || state == "list-success" || state == "save-error"
			var got error
			log := captureExportCall(t, func() { got = RunChangeDirectoryWithError(target, list) })
			success := state == "success" || state == "relative" || state == "list-success"
			changed := success || state == "list-error"
			if (got == nil) != success {
				t.Fatalf("unexpected result: %v", got)
			}
			wantDirectory := "/old"
			if changed {
				wantDirectory = resolved
			}
			if GetActiveUser().Workdir != wantDirectory || strings.Contains(log, "改变工作目录") != changed {
				t.Errorf("directory outcome mismatch: directory=%q log=%q", GetActiveUser().Workdir, log)
			}
			wantRequests := 1
			if state == "list-success" || state == "list-error" {
				wantRequests = 2
			}
			if requests != wantRequests {
				t.Errorf("requests=%d want %d", requests, wantRequests)
			}
			if state == "save-error" {
				var pathErr *os.PathError
				if !errors.As(got, &pathErr) {
					t.Errorf("save cause lost: %v", got)
				}
				if data, err := os.ReadFile(name + ".retained"); err != nil || string(data) != string(before) {
					t.Errorf("retained config changed: %v", err)
				}
			} else {
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var saved struct {
					Users []struct {
						Workdir string `json:"workdir"`
					} `json:"baidu_user_list"`
				}
				if err := json.Unmarshal(data, &saved); err != nil {
					t.Fatal(err)
				}
				if len(saved.Users) != 1 || saved.Users[0].Workdir != wantDirectory {
					t.Errorf("incorrect persisted directory: %+v", saved)
				}
				if !changed && string(data) != string(before) {
					t.Error("failed inspection rewrote configuration")
				}
			}
			if state == "metadata-error" || state == "list-error" || state == "glob-error" {
				var remote pcserror.Error
				code := 2
				if state == "metadata-error" {
					code = 31045
				}
				if !errors.As(got, &remote) || remote.GetRemoteErrCode() != code {
					t.Errorf("remote cause lost: %v", got)
				}
			}
			if state == "glob-empty" && !errors.Is(got, ErrShellPatternNoHit) {
				t.Errorf("no-match cause lost: %v", got)
			}
			if state == "glob-many" && !errors.Is(got, ErrShellPatternMultiRes) {
				t.Errorf("ambiguity cause lost: %v", got)
			}
		})
	}
}

func TestLsReturnsErrorsWithoutChangingDirectory(t *testing.T) {
	for _, state := range []string{"success", "remote", "network"} {
		t.Run(state, func(t *testing.T) {
			name := changeDirectoryFixture(t)
			before, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			cause := errors.New("fixture list transport failure")
			requests := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Path != "/api/list" || req.URL.Query().Get("dir") != "/target" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				if state == "network" {
					return nil, cause
				}
				payload := `{"errno":0,"list":[]}`
				if state == "remote" {
					payload = `{"errno":2}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			var got error
			log := captureExportCall(t, func() { got = RunLsWithError("/target", nil, nil) })
			if (got == nil) != (state == "success") || strings.Contains(log, "当前目录") != (state == "success") {
				t.Errorf("listing outcome mismatch: %v log=%q", got, log)
			}
			if state == "network" && !errors.Is(got, cause) {
				t.Errorf("transport cause lost: %v", got)
			}
			if requests != 1 || GetActiveUser().Workdir != "/old" {
				t.Errorf("listing changed directory or request count: %q/%d", GetActiveUser().Workdir, requests)
			}
			if data, err := os.ReadFile(name); err != nil || string(data) != string(before) {
				t.Errorf("listing changed config: %v", err)
			}
		})
	}
}
