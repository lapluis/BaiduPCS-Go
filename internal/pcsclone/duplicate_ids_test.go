package pcsclone

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
)

func TestBaiduIDCommandsUseUniqueTargets(t *testing.T) {
	for _, command := range []string{"offline/query", "offline/cancel", "offline/delete", "recycle/restore", "recycle/delete"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", command, jsonOutput), func(t *testing.T) {
				requests := make(chan []int64, 8)
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					var ids []int64
					payload := `{"errno":0,"error_code":0}`
					switch command {
					case "recycle/restore":
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
						}
						var param baidupcs.FsIDListJSON
						if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil {
							t.Error(err)
						}
						for _, item := range param.List {
							ids = append(ids, item.FsID)
						}
						payload = `{"error_code":0,"extra":{"list":[{"fs_id":8},{"fs_id":7}]}}`
					case "offline/query":
						for _, text := range strings.Split(r.URL.Query().Get("task_ids"), ",") {
							id, err := strconv.ParseInt(text, 10, 64)
							if err != nil {
								t.Error(err)
							}
							ids = append(ids, id)
						}
						payload = `{"error_code":0,"task_info":{"8":{"status":"1","file_size":"1","finished_size":"0","task_name":"eight"},"7":{"status":"1","file_size":"1","finished_size":"0","task_name":"seven"}}}`
					case "offline/cancel", "offline/delete":
						id, err := strconv.ParseInt(r.URL.Query().Get("task_id"), 10, 64)
						if err != nil {
							t.Error(err)
						}
						ids = []int64{id}
					default:
						key := "fidlist"
						if err := r.ParseForm(); err != nil {
							t.Error(err)
						}
						if err := json.Unmarshal([]byte(r.FormValue(key)), &ids); err != nil {
							t.Error(err)
						}
					}
					requests <- ids
					_, _ = io.WriteString(w, payload)
				}, command, []string{"bd:", "8", "7", "08", "+7", "8"}, flags)
				if err := c.cmd.Run(c); err != nil {
					t.Fatal(err)
				}
				want := []int64{8, 7}
				calls := len(requests)
				var sent []int64
				for i := 0; i < calls; i++ {
					sent = append(sent, (<-requests)...)
				}
				wantCalls := 1
				if command == "offline/cancel" || command == "offline/delete" {
					wantCalls = 2
				}
				if calls != wantCalls || !reflect.DeepEqual(sent, want) {
					t.Errorf("duplicate targets sent: calls=%d want=%d ids=%v", calls, wantCalls, sent)
				}
				if jsonOutput {
					if command == "offline/query" {
						var tasks []offlineTaskView
						if err := json.Unmarshal(out.Bytes(), &tasks); err != nil || len(tasks) != 2 || tasks[0].TaskID != 8 || tasks[1].TaskID != 7 {
							t.Errorf("query duplicates or order changed: %q %v", out.String(), err)
						}
					} else {
						var result struct {
							Deleted, Restored, Succeeded []int64
							Failed                       int
						}
						if err := json.Unmarshal(out.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						switch command {
						case "recycle/delete":
							if !reflect.DeepEqual(result.Deleted, want) {
								t.Errorf("duplicate deleted IDs: %q", out.String())
							}
						case "recycle/restore":
							if !reflect.DeepEqual(result.Restored, want) {
								t.Errorf("restore order changed: %q", out.String())
							}
						default:
							if result.Failed != 0 || !reflect.DeepEqual(result.Succeeded, want) {
								t.Errorf("duplicate offline results: %q", out.String())
							}
						}
					}
				} else {
					var expected string
					switch command {
					case "recycle/delete":
						expected = "Permanently deleted 2 item(s)\n"
					case "recycle/restore":
						expected = "Restored 2 item(s)\n"
					case "offline/query":
						lines := strings.Split(strings.TrimSpace(out.String()), "\n")
						if len(lines) != 2 || !strings.HasPrefix(lines[0], "task_id=8 ") || !strings.HasPrefix(lines[1], "task_id=7 ") {
							t.Errorf("duplicate query rows: %q", out.String())
						}
					default:
						action := strings.TrimPrefix(command, "offline/")
						expected = fmt.Sprintf("%s task 8: succeeded\n%s task 7: succeeded\n", action, action)
					}
					if expected != "" && out.String() != expected {
						t.Errorf("duplicate count or rows: %q want=%q", out.String(), expected)
					}
				}
			})
		}
	}
}

func TestParseIDsDeduplicatesAfterNumericValidation(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    []int64
		invalid bool
	}{
		{[]string{"8", "7", "08", "+7", "8"}, []int64{8, 7}, false},
		{[]string{"9223372036854775807", "7", "+9223372036854775807"}, []int64{9223372036854775807, 7}, false},
		{nil, []int64{}, false},
		{[]string{"7", "7", "bad"}, nil, true},
		{[]string{"7", "0"}, nil, true},
		{[]string{"7", "9223372036854775808"}, nil, true},
	} {
		got, err := parseIDs(tc.args)
		if (err != nil) != tc.invalid || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parse %v: ids=%v err=%v want=%v", tc.args, got, err, tc.want)
		}
	}
}
