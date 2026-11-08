package pcsclone

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestSizeCommandRejectsOverflowAndPreservesExactLimit(t *testing.T) {
	for _, jsonOutput := range []bool{false, true} {
		for _, tc := range []struct {
			name, second string
			invalid      bool
			exclude      bool
		}{
			{"exact-limit", "0", false, false},
			{"one-byte-over", "1", true, false},
			{"two-maxima", "9223372036854775807", true, false},
			{"filtered-valid", "1", false, true},
		} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOutput), func(t *testing.T) {
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				count := int64(2)
				if tc.exclude {
					flags = append(flags, "--exclude", "b")
					count = 1
				}
				calls := 0
				c, out := commandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Path != "/api/list" {
						t.Errorf("unexpected request: %s", r.URL.Path)
					}
					fmt.Fprintf(w, `{"errno":0,"list":[{"fs_id":1,"path":"/a","size":9223372036854775807},{"fs_id":2,"path":"/b","size":%s}]}`, tc.second)
				}, topLevelCommands(), "size", []string{"bd:"}, flags)
				err := c.cmd.Run(c)
				if calls != 1 {
					t.Fatalf("list requests = %d, want 1", calls)
				}
				if tc.invalid {
					if err == nil || out.Len() != 0 {
						t.Fatalf("overflow reported success: err=%v output=%q", err, out.String())
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if jsonOutput {
					var got map[string]int64
					if err := json.Unmarshal(out.Bytes(), &got); err != nil || got["bytes"] != 9223372036854775807 || got["count"] != count {
						t.Fatalf("exact total changed: %q, %v", out.String(), err)
					}
				} else if !strings.Contains(out.String(), "Bytes: 9223372036854775807") {
					t.Fatalf("exact total changed: %q", out.String())
				}
			})
		}
	}
}
