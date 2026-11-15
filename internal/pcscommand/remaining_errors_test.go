package pcscommand

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func remainingResponse(req *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}

func TestLocateCommandErrorsAndPartialResults(t *testing.T) {
	for _, state := range []string{"success", "remote", "network", "http", "malformed", "empty", "unusable", "partial", "multiple", "glob-empty"} {
		t.Run(state, func(t *testing.T) {
			changeDirectoryFixture(t)
			causes := []error{errors.New("first locate failure"), errors.New("second locate failure")}
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if state == "glob-empty" {
					if req.URL.Path != "/api/list" {
						t.Fatalf("queried empty selection: %s", req.URL)
					}
					return remainingResponse(req, `{"errno":0,"list":[]}`), nil
				}
				if req.URL.Query().Get("method") != "locatedownload" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				name := req.URL.Query().Get("path")
				if state == "network" || name == "/bad-one" {
					return nil, causes[0]
				}
				if name == "/bad-two" {
					return nil, causes[1]
				}
				body := `{"error_code":0,"urls":[{"url":"http://download.fixture.invalid/file","encrypt":0}]}`
				switch state {
				case "remote":
					body = `{"error_code":31045}`
				case "malformed":
					body = `{`
				case "empty":
					body = `{"error_code":0,"urls":[]}`
				case "unusable":
					body = `{"error_code":0,"urls":[{"url":"/relative","encrypt":0}]}`
				}
				resp := remainingResponse(req, body)
				if state == "http" {
					resp.StatusCode = 503
				}
				return resp, nil
			})
			paths := []string{"/fixture"}
			wantCalls := 1
			if state == "partial" {
				paths = []string{"/bad-one", "/good"}
				wantCalls = 2
			}
			if state == "multiple" {
				paths = []string{"/bad-one", "/bad-two", "/good"}
				wantCalls = 3
			}
			if state == "glob-empty" {
				paths = []string{"/fixture/*"}
			}
			var err error
			out := captureExportCall(t, func() { err = RunLocateDownloadWithError(paths, nil) })
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			if state == "success" {
				if err != nil || !strings.Contains(out, "download.fixture.invalid") {
					t.Fatalf("valid link failed: %v %q", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("failure lost: %q", out)
			}
			if state == "partial" || state == "multiple" {
				if !errors.Is(err, causes[0]) || !strings.Contains(out, "download.fixture.invalid") {
					t.Fatalf("partial result/cause lost: %v %q", err, out)
				}
				if state == "multiple" && !errors.Is(err, causes[1]) {
					t.Fatalf("second cause lost: %v", err)
				}
			} else if out != "" {
				t.Fatalf("failure printed success: %q", out)
			}
			if state == "network" && !errors.Is(err, causes[0]) {
				t.Fatalf("transport cause lost: %v", err)
			}
			if state == "glob-empty" && !errors.Is(err, ErrShellPatternNoHit) {
				t.Fatalf("matching cause lost: %v", err)
			}
		})
	}
}

func TestLocatePanCommandCoverage(t *testing.T) {
	for _, state := range []string{"success", "missing", "duplicate", "relative", "meta-partial", "meta-empty", "remote", "malformed", "meta-network", "link-network", "unrequested"} {
		t.Run(state, func(t *testing.T) {
			changeDirectoryFixture(t)
			cause := errors.New("fixture pan locate transport failure")
			calls := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Query().Get("method") == "meta" {
					if state == "meta-network" {
						return nil, cause
					}
					list := []map[string]interface{}{{"fs_id": 101, "path": "/one", "size": 4}, {"fs_id": 202, "path": "/two", "size": 4}}
					switch state {
					case "relative":
						list[0]["path"] = "one"
					case "duplicate":
						list[1] = list[0]
					case "meta-partial":
						list = list[:1]
					case "meta-empty":
						list = nil
					}
					if list == nil {
						return remainingResponse(req, `{"error_code":0,"list":[]}`), nil
					}
					data, _ := json.Marshal(map[string]interface{}{"error_code": 0, "list": list})
					return remainingResponse(req, string(data)), nil
				}
				if req.URL.Path == "/disk/home" {
					return remainingResponse(req, `{"sign1":"fixture","sign3":"key","timestamp":123,}`), nil
				}
				if req.URL.Path != "/api/download" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				if state == "link-network" {
					return nil, cause
				}
				if state == "remote" {
					return remainingResponse(req, `{"errno":31045}`), nil
				}
				if state == "malformed" {
					return remainingResponse(req, `{`), nil
				}
				links := `[{"fs_id":"00101","dlink":"https://download.fixture.invalid/one"},{"fs_id":"202","dlink":"https://download.fixture.invalid/two"}]`
				if state == "missing" || state == "duplicate" || state == "meta-partial" {
					links = `[{"fs_id":"101","dlink":"https://download.fixture.invalid/one"}]`
				}
				if state == "unrequested" {
					links = `[{"fs_id":"999","dlink":"https://download.fixture.invalid/other"}]`
				}
				return remainingResponse(req, `{"errno":0,"dlink":`+links+`}`), nil
			})
			paths := []string{"/one", "/two"}
			pcs := baidupcs.NewPCSWithClient(1, client)
			if state == "duplicate" {
				paths[1] = "/one"
			}
			var err error
			out := captureExportCall(t, func() { err = locateResolvedPaths(pcs, paths, &LocateDownloadOption{FromPan: true}) })
			wantCalls := 3
			if state == "meta-empty" || state == "meta-network" {
				wantCalls = 1
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d err=%v", calls, wantCalls, err)
			}
			switch state {
			case "success", "relative", "duplicate":
				if err != nil || !strings.Contains(out, "download.fixture.invalid/one") {
					t.Fatalf("valid mappings failed: %v %q", err, out)
				}
				if state == "duplicate" && strings.Count(out, "https://download.fixture.invalid/one") != 2 {
					t.Fatalf("duplicate selection lost: %q", out)
				}
			case "missing", "meta-partial":
				if err == nil || !strings.Contains(err.Error(), "/two") || !strings.Contains(out, "download.fixture.invalid/one") {
					t.Fatalf("partial link/meta lost: %v %q", err, out)
				}
			default:
				if err == nil || out != "" {
					t.Fatalf("failure lost/printed links: %v %q", err, out)
				}
			}
			if strings.HasSuffix(state, "network") && !errors.Is(err, cause) {
				t.Fatalf("transport cause lost: %v", err)
			}
		})
	}
}

func TestShareSetCommandResults(t *testing.T) {
	for _, state := range []string{"plain", "combined", "remote", "network", "malformed", "empty", "nil"} {
		t.Run(state, func(t *testing.T) {
			exportFixture(t)
			opt := &baidupcs.ShareOption{Password: "Ab12", Period: 7, IsCombined: state == "combined"}
			cause := errors.New("fixture share creation failure")
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if state == "empty" {
					if req.URL.Path != "/api/list" {
						t.Fatalf("shared empty selection: %s", req.URL)
					}
					return remainingResponse(req, `{"errno":0,"list":[]}`), nil
				}
				if req.URL.Path != "/share/pset" {
					t.Fatalf("unexpected share request: %s", req.URL)
				}
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				if req.Form.Get("pwd") != "Ab12" || req.Form.Get("period") != "7" {
					t.Errorf("changed creation parameters: %v", req.Form)
				}
				opt.Password = "zzzz"
				opt.IsCombined = !opt.IsCombined
				if state == "network" {
					return nil, cause
				}
				body := `{"errno":0,"shareid":101,"link":"https://pan.baidu.com/s/1fixture"}`
				if state == "remote" {
					body = `{"errno":110}`
				}
				if state == "malformed" {
					body = `{`
				}
				return remainingResponse(req, body), nil
			})
			paths := []string{"/fixture"}
			if state == "empty" {
				paths = []string{"/fixture/*"}
			}
			if state == "nil" {
				opt = nil
			}
			var err error
			out := captureExportCall(t, func() { err = RunShareSetWithError(paths, opt) })
			wantCalls := 1
			if state == "nil" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			if state == "plain" || state == "combined" {
				if err != nil || !strings.Contains(out, "Ab12") || (state == "combined") != strings.Contains(out, "?pwd=Ab12") {
					t.Fatalf("success/output snapshot changed: %v %q", err, out)
				}
			} else if err == nil || out != "" {
				t.Fatalf("failure lost/printed share: %v %q", err, out)
			}
			if state == "network" && !errors.Is(err, cause) {
				t.Fatalf("transport cause lost: %v", err)
			}
			if state == "empty" && !errors.Is(err, ErrShellPatternNoHit) {
				t.Fatalf("empty selection cause lost: %v", err)
			}
		})
	}
}

func TestShareTransferCommandDownloadOutcome(t *testing.T) {
	for _, state := range []string{"nil-options", "no-download", "download-success", "download-failure", "transfer-failure", "invalid"} {
		t.Run(state, func(t *testing.T) {
			changeDirectoryFixture(t)
			opt := &baidupcs.TransferOption{Download: strings.HasPrefix(state, "download-")}
			if state == "nil-options" {
				opt = nil
			}
			calls, waits, downloads := 0, 0, 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if state == "no-download" {
					opt.Download = true
				}
				body := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
				switch req.URL.Path {
				case "/share/verify":
					body = `{"errno":0}`
				case "/share/list":
					body = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a.txt"},{"fs_id":2,"server_filename":"b.txt"}]}`
				case "/share/transfer":
					body = `{"errno":0,"info":[{"fsid":1,"path":"/old/a.txt"},{"fsid":2,"path":"/old/b.txt"}]}`
					if state == "transfer-failure" {
						body = `{"errno":31045}`
					}
				}
				return remainingResponse(req, body), nil
			})
			cause := errors.New("fixture automatic download failure")
			params := []string{"https://pan.baidu.com/s/1fixture?pwd=Ab12"}
			if state == "invalid" {
				params = []string{"https://foreign.invalid/s/1fixture?pwd=Ab12"}
			}
			var err error
			out := captureExportCall(t, func() {
				err = runShareTransferWithError(params, opt, func(d time.Duration) {
					waits++
					if d != 10*time.Second {
						t.Errorf("wait=%v", d)
					}
				}, func(paths []string, options *DownloadOptions) error {
					downloads++
					if !reflect.DeepEqual(paths, []string{"/old/a.txt", "/old/b.txt"}) || options != nil {
						t.Errorf("wrong download request: %v %+v", paths, options)
					}
					if state == "download-failure" {
						return cause
					}
					return nil
				})
			})
			if state == "invalid" {
				if err == nil || calls != 0 || out != "" {
					t.Fatalf("invalid transfer reached network: %v calls=%d out=%q", err, calls, out)
				}
			} else {
				if calls != 5 {
					t.Fatalf("transfer calls=%d want=5 err=%v", calls, err)
				}
				if state == "transfer-failure" {
					if err == nil || out != "" {
						t.Fatalf("transfer failure lost: %v %q", err, out)
					}
				} else if !strings.Contains(out, "成功") {
					t.Fatalf("transfer success missing: %v %q", err, out)
				}
			}
			wantDownloads := 0
			if strings.HasPrefix(state, "download-") {
				wantDownloads = 1
			}
			if waits != wantDownloads || downloads != wantDownloads {
				t.Fatalf("wrong post-transfer actions: waits=%d downloads=%d", waits, downloads)
			}
			if state == "download-failure" {
				if !errors.Is(err, cause) || !strings.Contains(err.Error(), "转存已完成") {
					t.Fatalf("download failure/stage lost: %v", err)
				}
			} else if state != "invalid" && state != "transfer-failure" && err != nil {
				t.Fatalf("valid transfer failed: %v", err)
			}
		})
	}
}

func TestSumFileCommandBatchOutcomes(t *testing.T) {
	for _, state := range []string{"success", "mixed", "all-failed", "empty"} {
		t.Run(state, func(t *testing.T) {
			exportFixture(t)
			dir := t.TempDir()
			good := filepath.Join(dir, "a # b.bin")
			bad1 := filepath.Join(dir, "missing-one")
			bad2 := filepath.Join(dir, "missing-two")
			if err := os.WriteFile(good, []byte("abcd"), 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{good}
			switch state {
			case "mixed":
				paths = []string{bad1, good}
			case "all-failed":
				paths = []string{bad1, bad2}
			case "empty":
				paths = nil
			}
			var err error
			out := captureExportCall(t, func() { err = RunSumFileWithError(paths, "fixture-pcs") })
			if state == "success" || state == "mixed" {
				if !strings.Contains(out, exportFixtureMD5) || !strings.Contains(out, "3984772369") || !strings.Contains(out, "fixture-pcs rapidupload") || !strings.Contains(out, "4#a___b.bin") {
					t.Fatalf("checksum/format lost: err=%v out=%q", err, out)
				}
			}
			if state == "success" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatalf("failed batch returned success: %q", out)
			}
			if state == "mixed" || state == "all-failed" {
				var pathErr *os.PathError
				if !errors.Is(err, os.ErrNotExist) || !errors.As(err, &pathErr) || pathErr.Path != bad1 {
					t.Fatalf("file cause lost: %v", err)
				}
			}
			if state == "all-failed" && (!strings.Contains(err.Error(), bad2) || out != "") {
				t.Fatalf("second error lost or output invented: %v %q", err, out)
			}
			if data, readErr := os.ReadFile(good); readErr != nil || string(data) != "abcd" {
				t.Fatalf("source changed: %q %v", data, readErr)
			}
		})
	}
}
