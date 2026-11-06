package baidupcs

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func invokeCloudWrite(pcs *BaiduPCS, ctx context.Context, action string, legacy bool) (int64, error) {
	switch action {
	case "add":
		if legacy {
			return pcs.CloudDlAddTask("http://fixture.invalid/one", "/save/")
		}
		return pcs.CloudDlAddTaskWithContext(ctx, "http://fixture.invalid/one", "/save/")
	case "clear":
		var n int
		var err error
		if legacy {
			n, err = pcs.CloudDlClearTask()
		} else {
			n, err = pcs.CloudDlClearTaskWithContext(ctx)
		}
		return int64(n), err
	case "cancel":
		if legacy {
			return 0, pcs.CloudDlCancelTask(7)
		}
		return 0, pcs.CloudDlCancelTaskWithContext(ctx, 7)
	default:
		if legacy {
			return 0, pcs.CloudDlDeleteTask(7)
		}
		return 0, pcs.CloudDlDeleteTaskWithContext(ctx, 7)
	}
}

func TestCloudDlWriteResponsesAndCancellation(t *testing.T) {
	for _, action := range []string{"add", "cancel", "delete", "clear"} {
		for _, mode := range []string{"legacy-success", "context-success", "before", "canceled-reply", "remote-error", "invalid-json"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "before" {
					cancel()
				}
				calls := 0
				var body *locateBody
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != http.MethodPost || r.URL.Query().Get("method") != action+"_task" {
						t.Errorf("wrong endpoint: %s %s", r.Method, r.URL)
					}
					if action == "add" && (r.URL.Query().Get("source_url") != "http://fixture.invalid/one" || r.URL.Query().Get("save_path") != "/save/") {
						t.Error("creation parameters changed")
					}
					if (action == "cancel" || action == "delete") && r.URL.Query().Get("task_id") != "7" {
						t.Error("task ID changed")
					}
					payload := `{"error_code":0,"task_id":7,"total":3}`
					if mode == "remote-error" {
						payload = `{"error_code":31066}`
					}
					if mode == "invalid-json" {
						payload = `{`
					}
					body = &locateBody{Reader: strings.NewReader(payload)}
					if mode == "canceled-reply" {
						body.Reader = searchCancelReader{body.Reader, cancel}
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				value, err := invokeCloudWrite(pcs, ctx, action, mode == "legacy-success")
				if mode == "before" {
					if calls != 0 || !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled write sent: calls=%d err=%v", calls, err)
					}
					return
				}
				if calls != 1 || body == nil || !body.closed {
					t.Fatalf("wrong request/body ownership: calls=%d body=%+v", calls, body)
				}
				switch mode {
				case "legacy-success", "context-success":
					want := int64(0)
					if action == "add" {
						want = 7
					}
					if action == "clear" {
						want = 3
					}
					if err != nil || value != want {
						t.Fatalf("write result changed: value=%d want=%d err=%v", value, want, err)
					}
				case "canceled-reply":
					if !errors.Is(err, context.Canceled) || value != 0 {
						t.Fatalf("canceled success accepted: value=%d err=%v", value, err)
					}
				default:
					if err == nil || value != 0 {
						t.Fatalf("error accepted: value=%d err=%v", value, err)
					}
				}
			})
		}
	}
}
