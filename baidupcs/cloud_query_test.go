package baidupcs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestCloudDlQueryTaskDoesNotTruncateIDs(t *testing.T) {
	ids := make([]int64, 251)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	calls := 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		queryIDs := strings.Split(r.URL.Query().Get("task_ids"), ",")
		if len(queryIDs) > 100 {
			t.Errorf("query exceeds endpoint limit: %d", len(queryIDs))
		}
		info := map[string]interface{}{}
		for _, id := range queryIDs {
			info[id] = map[string]string{"status": "1", "task_name": "task-" + id}
		}
		payload, _ := json.Marshal(map[string]interface{}{"error_code": 0, "task_info": info})
		return response(r, string(payload)), nil
	})
	tasks, err := pcs.CloudDlQueryTask(ids)
	if err != nil || len(tasks) != len(ids) || calls != 3 {
		t.Fatalf("query truncated: tasks=%d want=%d calls=%d err=%v", len(tasks), len(ids), calls, err)
	}
	for i, task := range tasks {
		if task.TaskID != ids[i] || task.TaskName != "task-"+strconv.FormatInt(ids[i], 10) {
			t.Fatalf("query order/result changed at %d: %+v", i, task)
		}
	}
}

func TestCloudDlQueryBatchFailureDiscardsPartialResults(t *testing.T) {
	for _, mode := range []string{"cancel", "remote-error", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			var bodies []*locateBody
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				calls++
				payload := `{"error_code":0,"task_info":{"1":{"status":"1"}}}`
				if calls == 2 {
					switch mode {
					case "cancel":
						cancel()
					case "remote-error":
						payload = `{"error_code":31066}`
					case "invalid-json":
						payload = `{`
					}
				}
				body := &locateBody{Reader: strings.NewReader(payload)}
				bodies = append(bodies, body)
				resp := response(r, "")
				resp.Body = body
				return resp, nil
			})
			ids := make([]int64, 251)
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			tasks, err := pcs.CloudDlQueryTaskWithContext(ctx, ids)
			if err == nil || tasks != nil || calls != 2 {
				t.Fatalf("partial query accepted: tasks=%v err=%v calls=%d", tasks, err, calls)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			if mode == "remote-error" && err.GetRemoteErrCode() != 31066 {
				t.Fatalf("remote error lost: %v", err)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("batch response not closed")
				}
			}
		})
	}
}

func TestCloudDlReadLegacyResponses(t *testing.T) {
	for _, action := range []string{"list", "query"} {
		for _, mode := range []string{"tasks", "empty", "remote-error", "invalid-json"} {
			t.Run(action+"/"+mode, func(t *testing.T) {
				payload := `{"error_code":0,"task_info":[null,{"task_id":"bad"},{"task_id":"3","status":"1","file_size":"9","finished_size":"4","task_name":"three","file_list":[null,{"file_name":"part","file_size":"9"}]}]}`
				if action == "query" {
					payload = `{"error_code":0,"task_info":{"3":{"status":"1","file_size":"9","finished_size":"4","task_name":"three","file_list":[null,{"file_name":"part","file_size":"9"}]},"2":null}}`
				}
				switch mode {
				case "empty":
					if action == "list" {
						payload = `{"error_code":0,"task_info":[]}`
					} else {
						payload = `{"error_code":0,"task_info":{}}`
					}
				case "remote-error":
					payload = `{"error_code":31066}`
				case "invalid-json":
					payload = `{`
				}
				body := &locateBody{Reader: strings.NewReader(payload)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					method, wantHTTP := "list_task", http.MethodPost
					if action == "query" {
						method, wantHTTP = "query_task", http.MethodGet
						if r.URL.Query().Get("task_ids") != "3,2,3" {
							t.Error("query IDs changed")
						}
					}
					if r.URL.Query().Get("method") != method || r.Method != wantHTTP {
						t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL)
					}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var tasks CloudDlTaskList
				var err error
				if action == "list" {
					tasks, err = pcs.CloudDlListTask()
				} else {
					tasks, err = pcs.CloudDlQueryTask([]int64{3, 2, 3})
				}
				if !body.closed {
					t.Fatal("read response not closed")
				}
				if mode == "remote-error" || mode == "invalid-json" {
					if err == nil || tasks != nil {
						t.Fatalf("error accepted: tasks=%v err=%v", tasks, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				want := 1
				if action == "query" {
					want = 2
				}
				if mode == "empty" {
					want = 0
				}
				if len(tasks) != want {
					t.Fatalf("incorrect task count: %d want %d", len(tasks), want)
				}
				for _, task := range tasks {
					if task.TaskID != 3 || task.Status != 1 || task.FileSize != 9 || task.FinishedSize != 4 || task.TaskName != "three" || len(task.FileList) != 1 || task.FileList[0].FileSize != 9 {
						t.Fatalf("task conversion changed: %+v", task)
					}
				}
			})
		}
	}
}

func TestCloudDlReadCancellationDuringSuccessfulResponse(t *testing.T) {
	for _, action := range []string{"list", "query"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			body := &locateBody{Reader: searchCancelReader{strings.NewReader(`{"error_code":0}`), cancel}}
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				resp := response(r, "")
				resp.Body = body
				return resp, nil
			})
			var tasks CloudDlTaskList
			var err error
			if action == "list" {
				tasks, err = pcs.CloudDlListTaskWithContext(ctx)
			} else {
				tasks, err = pcs.CloudDlQueryTaskWithContext(ctx, []int64{1})
			}
			if !errors.Is(err, context.Canceled) || tasks != nil || !body.closed {
				t.Fatalf("canceled success accepted: tasks=%v err=%v closed=%v", tasks, err, body.closed)
			}
		})
	}
}
