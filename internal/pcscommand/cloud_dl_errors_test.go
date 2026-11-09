package pcscommand

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func offlineCommandForTest(op string, ids []int64, urls []string) error {
	switch op {
	case "add":
		return RunCloudDlAddTaskWithError(urls, "/fixture")
	case "query":
		return RunCloudDlQueryTaskWithError(ids)
	case "list":
		return RunCloudDlListTaskWithError()
	case "cancel":
		return RunCloudDlCancelTaskWithError(ids)
	case "delete":
		return RunCloudDlDeleteTaskWithError(ids)
	case "clear":
		return RunCloudDlClearTaskWithError()
	default:
		panic("unknown fixture operation")
	}
}

func offlineResponseForTest(req *http.Request, payload string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}
}

func TestOfflineCommandErrors(t *testing.T) {
	for _, op := range []string{"add", "query", "list", "cancel", "delete", "clear"} {
		for _, state := range []string{"success", "remote", "network", "malformed"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				exportFixture(t)
				calls := 0
				cause := errors.New("fixture offline transport failure")
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.URL.Query().Get("method") != op+"_task" {
						t.Fatalf("unexpected request: %s", req.URL)
					}
					if state == "network" {
						return nil, cause
					}
					payload := `{"error_code":0,"task_id":101,"total":2}`
					switch state {
					case "remote":
						payload = `{"error_code":31045}`
					case "malformed":
						payload = `{`
					case "success":
						if op == "query" {
							payload = `{"error_code":0,"task_info":{"101":{"task_name":"fixture"}}}`
						}
						if op == "list" {
							payload = `{"error_code":0,"task_info":[{"task_id":"101","task_name":"fixture"}]}`
						}
					}
					return offlineResponseForTest(req, payload), nil
				})
				var err error
				out := captureExportCall(t, func() { err = offlineCommandForTest(op, []int64{101}, []string{"magnet:?xt=urn:btih:fixture"}) })
				if calls != 1 {
					t.Fatalf("calls=%d want=1", calls)
				}
				if state == "success" {
					if err != nil || out == "" {
						t.Fatalf("success failed: err=%v out=%q", err, out)
					}
				} else {
					if err == nil || out != "" {
						t.Fatalf("failure lost or printed success: err=%v out=%q", err, out)
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

func TestOfflineCommandsValidateWholeBatch(t *testing.T) {
	for _, op := range []string{"query", "cancel", "delete"} {
		for _, ids := range [][]int64{nil, {101, 0}, {101, -1}} {
			t.Run(fmt.Sprintf("%s/%v", op, ids), func(t *testing.T) {
				exportFixture(t)
				calls := 0
				GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					return offlineResponseForTest(req, `{"error_code":0}`), nil
				})
				var err error
				out := captureExportCall(t, func() { err = offlineCommandForTest(op, ids, nil) })
				if err == nil || calls != 0 || out != "" {
					t.Fatalf("invalid batch reached API: err=%v calls=%d out=%q", err, calls, out)
				}
			})
		}
	}
	for _, urls := range [][]string{nil, {"https://fixture.invalid/one", "  "}} {
		t.Run(fmt.Sprintf("add/%v", urls), func(t *testing.T) {
			calls := exportFixture(t)
			if err := RunCloudDlAddTaskWithError(urls, "/fixture/*"); err == nil || *calls != 0 {
				t.Fatalf("invalid URLs reached path matching: err=%v calls=%d", err, *calls)
			}
		})
	}
}

func TestOfflineBatchContinuesAndKeepsAllCauses(t *testing.T) {
	for _, op := range []string{"add", "cancel", "delete"} {
		t.Run(op, func(t *testing.T) {
			exportFixture(t)
			causes := []error{errors.New("fixture first failure"), errors.New("fixture second failure")}
			calls := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls <= 2 {
					return nil, causes[calls-1]
				}
				return offlineResponseForTest(req, `{"error_code":0,"task_id":303}`), nil
			})
			var err error
			out := captureExportCall(t, func() {
				err = offlineCommandForTest(op, []int64{101, 202, 303}, []string{"http://fixture.invalid/one", "ed2k://fixture-two", "magnet:?xt=fixture-three"})
			})
			if calls != 3 || !errors.Is(err, causes[0]) || !errors.Is(err, causes[1]) || !strings.Contains(out, "成功") || !strings.Contains(out, "303") {
				t.Fatalf("partial outcome lost: calls=%d err=%v out=%q", calls, err, out)
			}
			if strings.Contains(out, "[101]") || strings.Contains(out, "[202]") {
				t.Fatalf("failed task reported success: %s", out)
			}
		})
	}
}

func TestOfflineBatchSnapshotsCallerInput(t *testing.T) {
	for _, op := range []string{"add", "cancel", "delete"} {
		t.Run(op, func(t *testing.T) {
			exportFixture(t)
			ids := []int64{101, 202}
			urls := []string{"https://fixture.invalid/one", "https://fixture.invalid/two"}
			var seen []string
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				value := req.URL.Query().Get("task_id")
				if op == "add" {
					value = req.URL.Query().Get("source_url")
				}
				seen = append(seen, value)
				if len(seen) == 1 {
					ids[1] = 999
					urls[1] = "https://fixture.invalid/reused"
				}
				return offlineResponseForTest(req, `{"error_code":0,"task_id":101}`), nil
			})
			var err error
			captureExportCall(t, func() { err = offlineCommandForTest(op, ids, urls) })
			want := []string{"101", "202"}
			if op == "add" {
				want = []string{"https://fixture.invalid/one", "https://fixture.invalid/two"}
			}
			if err != nil || !reflect.DeepEqual(seen, want) {
				t.Fatalf("caller reuse changed batch: err=%v seen=%v want=%v", err, seen, want)
			}
		})
	}
}

func TestOfflineAddStopsAfterPathMatchingFailure(t *testing.T) {
	exportFixture(t)
	cause := errors.New("fixture path lookup failure")
	calls := 0
	GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Query().Get("method") == "add_task" {
			t.Fatal("added after failed path matching")
		}
		return nil, cause
	})
	if err := RunCloudDlAddTaskWithError([]string{"https://fixture.invalid/one"}, "/fixture/*"); !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("path matching failure lost: calls=%d err=%v", calls, err)
	}
}
