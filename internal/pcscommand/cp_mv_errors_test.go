package pcscommand

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestCopyMoveErrorReturnsAndTargets(t *testing.T) {
	for _, op := range []string{"copy", "move"} {
		for _, state := range []string{"missing-31066", "missing-minus9", "directory", "directory-relative", "target-file", "mutation-error", "network", "denied", "no-source", "missing-many", "arity"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				requests := 0
				var wire []baidupcs.CpMvJSON
				transportErr := errors.New("fixture metadata transport failure")
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					requests++
					if req.Body != nil {
						defer req.Body.Close()
					}
					method := req.URL.Query().Get("method")
					payload := ""
					switch {
					case method == "meta":
						if state == "network" {
							return nil, transportErr
						}
						if state == "denied" {
							payload = `{"error_code":31045}`
						} else if strings.HasPrefix(state, "missing-") {
							code := 31066
							if state == "missing-minus9" {
								code = -9
							}
							payload = fmt.Sprintf(`{"error_code":%d}`, code)
						} else {
							p, dir := "/target", 1
							if state == "directory-relative" {
								p = "target"
							}
							if state == "target-file" {
								dir = 0
							}
							payload = fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":1,"path":%q,"isdir":%d}]}`, p, dir)
						}
					case req.URL.Path == "/api/list" && state == "no-source":
						payload = `{"errno":0,"list":[]}`
					case method == op:
						if err := req.ParseMultipartForm(4096); err != nil {
							t.Fatal(err)
						}
						if req.MultipartForm != nil {
							defer req.MultipartForm.RemoveAll()
						}
						var params struct {
							List []baidupcs.CpMvJSON `json:"list"`
						}
						if err := json.Unmarshal([]byte(req.FormValue("param")), &params); err != nil {
							t.Fatal(err)
						}
						wire = append(wire, params.List...)
						payload = `{"error_code":0}`
						if state == "mutation-error" {
							payload = `{"error_code":31061}`
						}
					default:
						t.Fatalf("unexpected request: %s", req.URL)
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				paths := []string{"/source", "/target"}
				if state == "directory" || state == "missing-many" {
					paths = []string{"/source", "/other", "/target"}
				}
				if state == "no-source" {
					paths = []string{"/source*", "/target"}
				}
				if state == "arity" {
					paths = []string{"/source"}
				}
				var got error
				log := captureExportCall(t, func() {
					if op == "copy" {
						got = RunCopyWithError(paths...)
					} else {
						got = RunMoveWithError(paths...)
					}
				})
				success := state == "missing-31066" || state == "missing-minus9" || state == "directory" || state == "directory-relative"
				if (got == nil) != success {
					t.Fatalf("unexpected result: %v", got)
				}
				if strings.Contains(log, "成功") != success {
					t.Errorf("incorrect success diagnostic: %q", log)
				}
				wantRequests := 1
				if success || state == "mutation-error" {
					wantRequests = 2
				}
				if state == "arity" {
					wantRequests = 0
				}
				if requests != wantRequests {
					t.Errorf("requests=%d want %d", requests, wantRequests)
				}
				if success || state == "mutation-error" {
					wantCount := 1
					if state == "directory" {
						wantCount = 2
					}
					if len(wire) != wantCount {
						t.Fatalf("wire entries=%v want %d", wire, wantCount)
					}
					for i, entry := range wire {
						wantTarget := "/target"
						if !strings.HasPrefix(state, "missing-") {
							wantTarget += "/" + strings.TrimPrefix(paths[i], "/")
						}
						if entry.From != paths[i] || entry.To != wantTarget {
							t.Errorf("incorrect mutation: %+v want %s -> %s", entry, paths[i], wantTarget)
						}
					}
				} else if len(wire) != 0 {
					t.Errorf("failed preflight mutated: %+v", wire)
				}
				if state == "network" && !errors.Is(got, transportErr) {
					t.Errorf("transport cause lost: %v", got)
				}
				if state == "no-source" && !errors.Is(got, ErrShellPatternNoHit) {
					t.Errorf("missing source error lost: %v", got)
				}
				if state == "missing-many" || state == "mutation-error" || state == "denied" {
					var remote pcserror.Error
					code := 31066
					if state == "mutation-error" {
						code = 31061
					}
					if state == "denied" {
						code = 31045
					}
					if !errors.As(got, &remote) || remote.GetRemoteErrCode() != code {
						t.Errorf("remote cause lost: %v", got)
					}
				}
			})
		}
	}
}
