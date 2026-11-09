package pcscommand

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func listResponseForTest(req *http.Request, payload string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}
}

func TestShareAndRecycleListErrors(t *testing.T) {
	for _, op := range []string{"share", "recycle"} {
		for _, state := range []string{"success", "remote", "network", "http", "malformed", "invalid"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				calls := 0
				cause := errors.New("fixture list transport failure")
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					wantPath := "/share/record"
					if op == "recycle" {
						wantPath = "/api/recycle/list"
					}
					if req.Method != http.MethodGet || req.URL.Path != wantPath || req.URL.Query().Get("page") != "2" {
						t.Fatalf("unexpected list request: %s %s", req.Method, req.URL)
					}
					if state == "network" {
						return nil, cause
					}
					payload := `{"errno":0,"list":[{"shareId":101,"public":1,"typicalPath":"/fixture-list-item"}]}`
					if op == "recycle" {
						payload = `{"errno":0,"list":[{"fs_id":101,"path":"/fixture-list-item","isdir":0,"size":4}]}`
					}
					switch state {
					case "remote":
						payload = `{"errno":31045}`
					case "malformed":
						payload = `{`
					case "invalid":
						payload = `{"errno":0,"list":[null]}`
					}
					response := listResponseForTest(req, payload)
					if state == "http" {
						response.StatusCode = 503
					}
					return response, nil
				})
				var err error
				out := captureExportCall(t, func() {
					if op == "share" {
						err = RunShareListWithError(2)
					} else {
						err = RunRecycleListWithError(2)
					}
				})
				if calls != 1 {
					t.Fatalf("calls=%d want=1", calls)
				}
				if state == "success" {
					if err != nil || !strings.Contains(out, "/fixture-list-item") {
						t.Fatalf("valid list failed: err=%v out=%q", err, out)
					}
				} else {
					if err == nil || out != "" {
						t.Fatalf("failure lost or printed list: err=%v out=%q", err, out)
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
				}
			})
		}
	}
}

func TestShareAndRecycleListPageNormalization(t *testing.T) {
	for _, op := range []string{"share", "recycle"} {
		for _, page := range []int{-1, 0, 3} {
			t.Run(fmt.Sprintf("%s/%d", op, page), func(t *testing.T) {
				exportFixture(t)
				calls := 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if got := req.URL.Query().Get("page"); got != strconv.Itoa(max(1, page)) {
						t.Errorf("page=%s want=%d", got, max(1, page))
					}
					return listResponseForTest(req, `{"errno":0,"list":[]}`), nil
				})
				var err error
				captureExportCall(t, func() {
					if op == "share" {
						err = RunShareListWithError(page)
					} else {
						err = RunRecycleListWithError(page)
					}
				})
				if err != nil || calls != 1 {
					t.Fatalf("empty list or normalized page failed: calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestShareListKeepsPartialDetailsAndAllCauses(t *testing.T) {
	for _, state := range []string{"success", "zero-password", "first-fails", "both-fail"} {
		t.Run(state, func(t *testing.T) {
			exportFixture(t)
			causes := []error{errors.New("fixture first password lookup failure"), errors.New("fixture second password lookup failure")}
			var details []string
			listCalls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/share/record" {
					listCalls++
					return listResponseForTest(req, `{"errno":0,"list":[{"shareId":101,"public":0,"typicalPath":"/one"},{"shareId":202,"public":0,"typicalPath":"/two"},{"shareId":303,"public":1,"typicalPath":"/public"},{"shareId":404,"public":0,"expiredType":-1,"typicalPath":"/expired"}]}`), nil
				}
				if req.URL.Path != "/share/surlinfoinrecord" || req.Method != http.MethodGet {
					t.Fatalf("unexpected detail request: %s %s", req.Method, req.URL)
				}
				id := req.URL.Query().Get("shareid")
				details = append(details, id)
				index := 0
				if id == "202" {
					index = 1
				} else if id != "101" {
					t.Fatalf("looked up public or expired share: %s", id)
				}
				if state == "both-fail" || state == "first-fails" && index == 0 {
					return nil, causes[index]
				}
				pwd := "  ab12  "
				if index == 1 {
					pwd = "  cd34  "
				} else if state == "zero-password" {
					pwd = "0"
				}
				return listResponseForTest(req, `{"errno":0,"pwd":"`+pwd+`"}`), nil
			})
			var err error
			out := captureExportCall(t, func() { err = RunShareListWithError(2) })
			if listCalls != 1 || !reflect.DeepEqual(details, []string{"101", "202"}) {
				t.Fatalf("lookup sequence changed: list=%d details=%v", listCalls, details)
			}
			for _, row := range []string{"/one", "/two", "/public", "/expired"} {
				if !strings.Contains(out, row) {
					t.Errorf("missing row %s: %q", row, out)
				}
			}
			wantFailures := 0
			switch state {
			case "first-fails":
				wantFailures = 1
			case "both-fail":
				wantFailures = 2
			default:
				if err != nil {
					t.Fatalf("successful details failed: %v", err)
				}
			}
			if strings.Count(out, "查询失败") != wantFailures {
				t.Errorf("incorrect failed password markers: %q", out)
			}
			if wantFailures > 0 && (!errors.Is(err, causes[0]) || !strings.Contains(err.Error(), "ShareID 101")) {
				t.Errorf("first detail failure lost: %v", err)
			}
			if wantFailures == 2 && (!errors.Is(err, causes[1]) || !strings.Contains(err.Error(), "ShareID 202")) {
				t.Errorf("second detail failure lost: %v", err)
			}
			if wantFailures < 2 && !strings.Contains(out, "cd34") {
				t.Errorf("later success lost after earlier failure: %q", out)
			}
			if state == "success" && !strings.Contains(out, "ab12") {
				t.Errorf("first password lost: %q", out)
			}
			if state == "zero-password" && strings.Contains(out, "ab12") {
				t.Errorf("zero password changed: %q", out)
			}
		})
	}
}
