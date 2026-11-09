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

func quotaMetaResponseForTest(req *http.Request, payload string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}
}

func TestQuotaCommandRatios(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total, used int64
		ratio       string
	}{
		{"half", 8, 4, "50.000000%"}, {"unused", 8, 0, "0.000000%"}, {"over-quota", 2, 3, "150.000000%"}, {"zero-empty", 0, 0, "--"}, {"zero-used", 0, 5, "--"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exportFixture(t)
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Query().Get("method") != "info" {
					t.Fatalf("unexpected quota request: %s %s", req.Method, req.URL)
				}
				return quotaMetaResponseForTest(req, fmt.Sprintf(`{"error_code":0,"quota":%d,"used":%d}`, tc.total, tc.used)), nil
			})
			var err error
			out := captureExportCall(t, func() { err = RunGetQuotaWithError() })
			if err != nil || calls != 1 || !strings.Contains(out, "比率: "+tc.ratio) || strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
				t.Fatalf("incorrect quota result: calls=%d err=%v out=%q", calls, err, out)
			}
		})
	}
}

func TestQuotaAndMetaCommandFailures(t *testing.T) {
	for _, op := range []string{"quota", "meta"} {
		for _, state := range []string{"remote", "network", "http", "malformed", "invalid"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				cause := errors.New("fixture quota/meta transport failure")
				calls := 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Body != nil {
						defer req.Body.Close()
					}
					if state == "network" {
						return nil, cause
					}
					payload := `{"error_code":31045}`
					if state == "malformed" {
						payload = `{`
					}
					if state == "invalid" {
						payload = `{"error_code":0,"quota":8}`
						if op == "meta" {
							payload = `{"error_code":0,"list":[null]}`
						}
					}
					response := quotaMetaResponseForTest(req, payload)
					if state == "http" {
						response.StatusCode = 503
					}
					return response, nil
				})
				var err error
				out := captureExportCall(t, func() {
					if op == "quota" {
						err = RunGetQuotaWithError()
					} else {
						err = RunGetMetaWithError("/fixture-file")
					}
				})
				if err == nil || calls != 1 || strings.Contains(out, "fs_id") || strings.Contains(out, exportFixtureMD5) || op == "quota" && out != "" {
					t.Fatalf("failure lost or printed data: calls=%d err=%v out=%q", calls, err, out)
				}
				if state == "network" && !errors.Is(err, cause) {
					t.Fatalf("transport cause lost: %v", err)
				}
				if state == "remote" {
					var remote pcserror.Error
					if !errors.As(err, &remote) || remote.GetRemoteErrCode() != 31045 {
						t.Fatalf("remote cause lost: %v", err)
					}
				}
				if op == "meta" && !strings.Contains(err.Error(), "/fixture-file") {
					t.Fatalf("failed path lost: %v", err)
				}
			})
		}
	}
}

func TestMetaCommandSelectionAndPartialResults(t *testing.T) {
	for _, state := range []string{"default", "empty-args", "glob-empty", "glob-error", "mixed", "partial"} {
		t.Run(state, func(t *testing.T) {
			if state == "default" {
				changeDirectoryFixture(t)
			} else {
				exportFixture(t)
			}
			cause := errors.New("fixture metadata lookup failure")
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Path == "/api/list" {
					if state == "glob-error" {
						return nil, cause
					}
					return quotaMetaResponseForTest(req, `{"errno":0,"list":[]}`), nil
				}
				if req.URL.Query().Get("method") != "meta" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				if err := req.ParseMultipartForm(4096); err != nil {
					t.Fatal(err)
				}
				if req.MultipartForm != nil {
					defer req.MultipartForm.RemoveAll()
				}
				var params baidupcs.PathsListJSON
				if err := json.Unmarshal([]byte(req.FormValue("param")), &params); err != nil || len(params.List) != 1 || params.List[0] == nil {
					t.Fatalf("bad metadata params: %v %+v", err, params)
				}
				path := params.List[0].Path
				if path == "/bad" {
					return nil, cause
				}
				want := "/old"
				if state == "mixed" {
					want = "/present"
				}
				if state == "partial" {
					want = "/first"
				}
				if path != want {
					t.Fatalf("unexpected or late metadata request: %s want=%s", path, want)
				}
				isdir := 0
				if state == "default" {
					isdir = 1
				}
				return quotaMetaResponseForTest(req, fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":101,"path":%q,"isdir":%d,"size":4,"md5":%q,"block_list":[%q]}]}`, path, isdir, exportFixtureMD5, exportFixtureMD5)), nil
			})
			paths := []string{""}
			wantCalls := 1
			switch state {
			case "empty-args":
				paths = nil
				wantCalls = 0
			case "glob-empty", "glob-error":
				paths = []string{"/fixture/*"}
			case "mixed":
				paths = []string{"/fixture/*", "/present"}
				wantCalls = 2
			case "partial":
				paths = []string{"/first", "/bad", "/never"}
				wantCalls = 2
			}
			var err error
			out := captureExportCall(t, func() { err = RunGetMetaWithError(paths...) })
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			switch state {
			case "empty-args", "glob-empty":
				if !errors.Is(err, ErrShellPatternNoHit) || out != "" {
					t.Fatalf("empty selection succeeded: err=%v out=%q", err, out)
				}
			case "glob-error":
				if !errors.Is(err, cause) || out != "" {
					t.Fatalf("matching failure lost: err=%v out=%q", err, out)
				}
			case "partial":
				if !errors.Is(err, cause) || !strings.Contains(err.Error(), "/bad") || !strings.Contains(out, exportFixtureMD5) || strings.Contains(out, "/never") {
					t.Fatalf("partial result or cause lost: err=%v out=%q", err, out)
				}
			default:
				if err != nil || !strings.Contains(out, "fs_id") {
					t.Fatalf("valid selection failed: err=%v out=%q", err, out)
				}
			}
		})
	}
}
