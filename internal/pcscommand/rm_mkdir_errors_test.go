package pcscommand

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRemoveMkdirErrorsAndWirePaths(t *testing.T) {
	for _, op := range []string{"remove", "mkdir"} {
		states := []string{"remote", "network", "malformed", "http", "success"}
		if op == "remove" {
			states = append(states, "glob-error", "glob-empty-mixed", "empty")
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				requests, mutations := 0, 0
				transportErr := errors.New("fixture mutation transport failure")
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						defer req.Body.Close()
					}
					payload := `{"error_code":0}`
					status := http.StatusOK
					if req.URL.Path == "/api/list" {
						if op != "remove" {
							t.Fatal("mkdir listed remote paths")
						}
						payload = `{"errno":0,"list":[]}`
						if state == "glob-error" {
							payload = `{"errno":2}`
						}
					} else {
						mutations++
						method := req.URL.Query().Get("method")
						if op == "mkdir" {
							if method != "mkdir" || req.URL.Query().Get("path") != "/fixture" {
								t.Fatalf("wrong mkdir request: %s", req.URL)
							}
						} else {
							if method != "delete" {
								t.Fatalf("unexpected request: %s", req.URL)
							}
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
							want := []string{"/one", "/two"}
							if state == "glob-empty-mixed" {
								want = want[:1]
							}
							if len(params.List) != len(want) {
								t.Fatalf("wrong delete batch: %+v", params)
							}
							for i, p := range params.List {
								if p == nil || p.Path != want[i] {
									t.Fatalf("wrong delete path: %+v", params)
								}
							}
						}
						switch state {
						case "network":
							return nil, transportErr
						case "remote":
							payload = `{"error_code":31045}`
						case "malformed":
							payload = `{`
						case "http":
							status = http.StatusServiceUnavailable
						}
					}
					return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				paths := []string{"/one", "/two"}
				if state == "glob-error" || state == "empty" {
					paths = []string{"/missing*"}
				}
				if state == "glob-empty-mixed" {
					paths = []string{"/missing*", "/one"}
				}
				var got error
				log := captureExportCall(t, func() {
					if op == "remove" {
						got = RunRemoveWithError(paths...)
					} else {
						got = RunMkdirWithError("/fixture")
					}
				})
				success := state == "success" || state == "glob-empty-mixed"
				if (got == nil) != success {
					t.Fatalf("unexpected operation result: %v", got)
				}
				if strings.Contains(log, "成功") != success {
					t.Errorf("incorrect success output: %q", log)
				}
				wantRequests, wantMutations := 1, 1
				if state == "glob-empty-mixed" {
					wantRequests = 2
				}
				if state == "glob-error" || state == "empty" {
					wantMutations = 0
				}
				if requests != wantRequests || mutations != wantMutations {
					t.Errorf("requests=%d mutations=%d want %d/%d", requests, mutations, wantRequests, wantMutations)
				}
				if state == "network" && !errors.Is(got, transportErr) {
					t.Errorf("transport cause lost: %v", got)
				}
				if state == "empty" && !errors.Is(got, ErrShellPatternNoHit) {
					t.Errorf("empty selection cause lost: %v", got)
				}
				if state == "remote" || state == "glob-error" {
					var remote pcserror.Error
					code := 31045
					if state == "glob-error" {
						code = 2
					}
					if !errors.As(got, &remote) || remote.GetRemoteErrCode() != code {
						t.Errorf("remote cause lost: %v", got)
					}
				}
			})
		}
	}
}
