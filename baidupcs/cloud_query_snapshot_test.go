package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestCloudDlQueryKeepsOriginalTaskBatches(t *testing.T) {
	for _, mode := range []string{"replace", "reverse", "clear"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", mode, contextual), func(t *testing.T) {
				ids := make([]int64, 205)
				for i := range ids {
					ids[i] = int64(i + 1)
				}
				calls, sent := 0, 0
				var bodies []*locateBody
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					batch := strings.Split(req.URL.Query().Get("task_ids"), ",")
					wantSize := min(100, 205-sent)
					if len(batch) != wantSize {
						t.Errorf("batch %d size=%d want=%d", calls, len(batch), wantSize)
					}
					var entries []string
					for i, id := range batch {
						if id != strconv.Itoa(sent+i+1) {
							t.Errorf("batch %d first changed task at %d: got=%s want=%d", calls, i, id, sent+i+1)
							break
						}
					}
					seen := make(map[string]bool)
					for _, id := range batch {
						if seen[id] {
							continue
						}
						seen[id] = true
						entries = append(entries, fmt.Sprintf(`%q:{"status":"1","file_size":"1","finished_size":"0","task_name":"fixture","file_list":[]}`, id))
					}
					sent += len(batch)
					if calls == 1 {
						switch mode {
						case "replace":
							for i := range ids {
								ids[i] = int64(10000 + i)
							}
						case "reverse":
							for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
								ids[i], ids[j] = ids[j], ids[i]
							}
						case "clear":
							clear(ids)
						}
					}
					body := &locateBody{Reader: strings.NewReader(`{"error_code":0,"task_info":{` + strings.Join(entries, ",") + `}}`)}
					bodies = append(bodies, body)
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				var tasks CloudDlTaskList
				var err pcserror.Error
				if contextual {
					tasks, err = pcs.CloudDlQueryTaskWithContext(context.Background(), ids)
				} else {
					tasks, err = pcs.CloudDlQueryTask(ids)
				}
				if err != nil || len(tasks) != 205 || calls != 3 || sent != 205 {
					t.Fatalf("batched query changed: count=%d calls=%d sent=%d err=%v", len(tasks), calls, sent, err)
				}
				for i, task := range tasks {
					if task.TaskID != int64(i+1) {
						t.Errorf("result order changed at %d: id=%d", i, task.TaskID)
						break
					}
				}
				for i, body := range bodies {
					if !body.closed {
						t.Errorf("response %d left open", i)
					}
				}
				wantFirst := int64(10000)
				if mode == "reverse" {
					wantFirst = 205
				} else if mode == "clear" {
					wantFirst = 0
				}
				if ids[0] != wantFirst {
					t.Fatal("query overwrote caller's reused IDs")
				}
			})
		}
	}
}
