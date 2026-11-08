package pcsclone

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOfflineCommandsRejectInvalidSuccessValues(t *testing.T) {
	for _, tc := range []struct{ action, payload string }{
		{"add", `{"error_code":0,"task_id":0}`},
		{"add", `{"error_code":0,"task_id":-1}`},
		{"add", `{"error_code":0}`},
		{"clear", `{"error_code":0,"total":-1}`},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/json=%t", tc.action, tc.payload, jsonOutput), func(t *testing.T) {
				args := []string{"bd:"}
				if tc.action == "add" {
					args = append(args, "https://fixture.invalid/source")
				}
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				calls := 0
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = io.WriteString(w, tc.payload) }, "offline/"+tc.action, args, flags)
				if err := c.cmd.Run(c); err == nil || calls != 1 {
					t.Fatalf("malformed result succeeded or retried: err=%v calls=%d out=%q", err, calls, out.String())
				}
				if tc.action == "add" && jsonOutput {
					var result offlineAddResult
					if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Added) != 0 || len(result.Errors) != 1 {
						t.Fatalf("invalid task counted as added: %q err=%v", out.String(), err)
					}
				} else if out.Len() != 0 {
					t.Fatalf("malformed result published success: %q", out.String())
				}
			})
		}
	}
}

func TestOfflineAddRetainsValidTasksAroundMalformedResult(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(jsonOutput), func(t *testing.T) {
			calls := 0
			var flags []string
			if jsonOutput {
				flags = []string{"--json"}
			}
			c, out := baiduCommandFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				id := map[int]int{1: 7, 2: 0, 3: 8}[calls]
				_, _ = fmt.Fprintf(w, `{"error_code":0,"task_id":%d}`, id)
			}, "offline/add", []string{"bd:", "https://fixture.invalid/one", "https://fixture.invalid/two", "https://fixture.invalid/three"}, flags)
			if err := c.cmd.Run(c); err == nil || calls != 3 {
				t.Fatalf("partial failure lost: calls=%d err=%v output=%q", calls, err, out.String())
			}
			if jsonOutput {
				var result offlineAddResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Added) != 2 || len(result.Errors) != 1 || result.Added[0].TaskID != 7 || result.Added[1].TaskID != 8 {
					t.Fatalf("wrong partial results: %q err=%v", out.String(), err)
				}
			} else if strings.Contains(out.String(), "task_id=0") || !strings.Contains(out.String(), "task_id=7") || !strings.Contains(out.String(), "task_id=8") || strings.Contains(out.String(), "fixture.invalid/two") {
				t.Fatalf("invalid task published: %q", out.String())
			}
		})
	}
}
