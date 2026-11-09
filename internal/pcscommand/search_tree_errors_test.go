package pcscommand

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func searchTreeResponseForTest(req *http.Request, payload string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}
}

func TestSearchAndTreeCommandOutcomes(t *testing.T) {
	for _, op := range []string{"search", "tree"} {
		for _, state := range []string{"success", "empty", "remote", "network", "http", "malformed", "invalid", "glob-empty", "glob-error", "glob-multi"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				cause := errors.New("fixture search/tree transport failure")
				calls := 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Method != http.MethodGet {
						t.Fatalf("unexpected HTTP method: %s", req.Method)
					}
					if strings.HasPrefix(state, "glob-") {
						if req.URL.Path != "/api/list" {
							t.Fatalf("query followed failed matching: %s", req.URL)
						}
						if state == "glob-error" {
							return nil, cause
						}
						payload := `{"errno":0,"list":[]}`
						if state == "glob-multi" {
							payload = `{"errno":0,"list":[{"fs_id":1,"path":"/fixture/one","server_filename":"one","isdir":1},{"fs_id":2,"path":"/fixture/two","server_filename":"two","isdir":1}]}`
						}
						return searchTreeResponseForTest(req, payload), nil
					}
					if op == "tree" {
						if req.URL.Path != "/api/list" || req.URL.Query().Get("dir") != "/fixture" {
							t.Fatalf("unexpected tree request: %s", req.URL)
						}
					} else if req.URL.Query().Get("method") != "search" || req.URL.Query().Get("path") != "/fixture" {
						t.Fatalf("unexpected search request: %s", req.URL)
					}
					if state == "network" {
						return nil, cause
					}
					payload := `{"error_code":0,"errno":0,"list":[{"fs_id":101,"path":"/fixture/item","server_filename":"item","size":4}]}`
					switch state {
					case "empty":
						payload = `{"error_code":0,"errno":0,"list":[]}`
					case "remote":
						payload = `{"error_code":31045,"errno":31045}`
					case "malformed":
						payload = `{`
					case "invalid":
						payload = `{"error_code":0,"errno":0,"list":[null]}`
					}
					response := searchTreeResponseForTest(req, payload)
					if state == "http" {
						response.StatusCode = 503
					}
					return response, nil
				})
				target := "/fixture"
				if strings.HasPrefix(state, "glob-") {
					target += "/*"
				}
				var err error
				out := captureExportCall(t, func() {
					if op == "search" {
						err = RunSearchWithError(target, "needle", nil)
					} else {
						err = RunTreeWithError(target, 0, nil)
					}
				})
				if calls != 1 {
					t.Fatalf("calls=%d want=1", calls)
				}
				if state == "success" || state == "empty" {
					if err != nil || state == "success" && !strings.Contains(out, "item") {
						t.Fatalf("valid result failed: err=%v out=%q", err, out)
					}
				} else {
					if err == nil || out != "" {
						t.Fatalf("failure lost or printed data: err=%v out=%q", err, out)
					}
					if state == "network" || state == "glob-error" {
						if !errors.Is(err, cause) {
							t.Fatalf("transport cause lost: %v", err)
						}
					}
					if state == "remote" {
						var remote pcserror.Error
						if !errors.As(err, &remote) || remote.GetRemoteErrCode() != 31045 {
							t.Fatalf("remote cause lost: %v", err)
						}
					}
					if state == "glob-empty" && !errors.Is(err, ErrShellPatternNoHit) {
						t.Fatalf("no-hit cause lost: %v", err)
					}
					if state == "glob-multi" && !errors.Is(err, ErrShellPatternMultiRes) {
						t.Fatalf("multi-match cause lost: %v", err)
					}
					if !strings.Contains(err.Error(), target) {
						t.Fatalf("failed path lost: %v", err)
					}
				}
			})
		}
	}
}

func TestSearchOptionsAndRelativePath(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(fmt.Sprint(full), func(t *testing.T) {
			changeDirectoryFixture(t)
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				wantRe := "0"
				if full {
					wantRe = "1"
				}
				q := req.URL.Query()
				if q.Get("method") != "search" || q.Get("path") != "/old/child" || q.Get("wd") != "关键字 +%" || q.Get("re") != wantRe {
					t.Fatalf("wrong search options: %s", req.URL)
				}
				return searchTreeResponseForTest(req, `{"error_code":0,"list":[{"fs_id":101,"path":"/old/child/item","server_filename":"item","size":4}]}`), nil
			})
			var opt *SearchOptions
			if full {
				opt = &SearchOptions{Recurse: true, Total: true}
			}
			var err error
			out := captureExportCall(t, func() { err = RunSearchWithError("child", "关键字 +%", opt) })
			if err != nil || calls != 1 || !strings.Contains(out, "/old/child/item") || full && !strings.Contains(out, "FS ID") {
				t.Fatalf("search settings changed: calls=%d err=%v out=%q", calls, err, out)
			}
		})
	}
}

func TestTreeKeepsPartialResultsAndAllCauses(t *testing.T) {
	for _, mode := range []string{"nil-options", "unlimited", "depth-zero", "depth-one"} {
		t.Run(mode, func(t *testing.T) {
			exportFixture(t)
			causes := []error{errors.New("fixture first directory failure"), errors.New("fixture second directory failure")}
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				switch req.URL.Query().Get("dir") {
				case "/fixture":
					return searchTreeResponseForTest(req, `{"errno":0,"list":[{"fs_id":1,"path":"/fixture/bad-one","server_filename":"bad-one","isdir":1},{"fs_id":2,"path":"/fixture/bad-two","server_filename":"bad-two","isdir":1},{"fs_id":3,"path":"/fixture/good","server_filename":"good","isdir":1}]}`), nil
				case "/fixture/bad-one":
					return nil, causes[0]
				case "/fixture/bad-two":
					return nil, causes[1]
				case "/fixture/good":
					return searchTreeResponseForTest(req, `{"errno":0,"list":[{"fs_id":4,"path":"/fixture/good/nested","server_filename":"nested","isdir":1},{"fs_id":5,"path":"/fixture/good/later-file","server_filename":"later-file","size":4}]}`), nil
				case "/fixture/good/nested":
					return searchTreeResponseForTest(req, `{"errno":0,"list":[{"fs_id":6,"path":"/fixture/good/nested/deep-file","server_filename":"deep-file","size":4}]}`), nil
				default:
					t.Fatalf("unexpected tree request: %s", req.URL)
					return nil, nil
				}
			})
			var opt *TreeOptions
			wantCalls := 5
			switch mode {
			case "unlimited":
				opt = &TreeOptions{Depth: -1, ShowFsid: true}
			case "depth-zero":
				opt = &TreeOptions{Depth: 0}
				wantCalls = 1
			case "depth-one":
				opt = &TreeOptions{Depth: 1, ShowFsid: true}
				wantCalls = 4
			}
			var err error
			out := captureExportCall(t, func() { err = RunTreeWithError("/fixture", 0, opt) })
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
			if mode == "depth-zero" {
				if err != nil || strings.Contains(out, "later-file") || !strings.Contains(out, "good/") {
					t.Fatalf("depth-zero changed: err=%v out=%q", err, out)
				}
				return
			}
			if !errors.Is(err, causes[0]) || !errors.Is(err, causes[1]) || !strings.Contains(err.Error(), "/fixture/bad-one") || !strings.Contains(err.Error(), "/fixture/bad-two") {
				t.Fatalf("directory causes lost: %v", err)
			}
			if !strings.Contains(out, "later-file") || strings.Contains(out, causes[0].Error()) || strings.Contains(out, causes[1].Error()) {
				t.Fatalf("partial output lost or mixed with errors: %q", out)
			}
			if (mode == "depth-one") == strings.Contains(out, "deep-file") {
				t.Fatalf("depth limit changed: %q", out)
			}
			if opt != nil && opt.ShowFsid && !strings.Contains(out, "later-file: 5") {
				t.Fatalf("file ID missing: %q", out)
			}
		})
	}
}

func TestTreeRejectsNegativeStartingDepth(t *testing.T) {
	calls := exportFixture(t)
	if err := RunTreeWithError("/fixture", -1, nil); err == nil || *calls != 0 {
		t.Fatalf("invalid depth reached API: err=%v calls=%d", err, *calls)
	}
}
