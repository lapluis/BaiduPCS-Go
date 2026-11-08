package pcsclone

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRecycleRestoreResultIdentityValidation(t *testing.T) {
	for _, tc := range []struct {
		name, list string
		invalid    bool
	}{
		{"valid", `[{"fs_id":7},{"fs_id":8}]`, false},
		{"zero", `[{"fs_id":7},{"fs_id":0}]`, true},
		{"negative", `[{"fs_id":7},{"fs_id":-1}]`, true},
		{"missing", `[{"fs_id":7},{}]`, true},
		{"duplicate", `[{"fs_id":7},{"fs_id":7}]`, true},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, jsonOutput), func(t *testing.T) {
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				calls := 0
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost || r.URL.Query().Get("method") != "restore" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					}
					_, _ = io.WriteString(w, `{"error_code":0,"extra":{"list":`+tc.list+`}}`)
				}, "recycle/restore", []string{"bd:", "7", "8"}, flags)
				err := c.cmd.Run(c)
				if calls != 1 {
					t.Fatalf("restore retried: calls=%d", calls)
				}
				if tc.invalid {
					if err == nil || out.Len() != 0 {
						t.Fatalf("invalid restore reported success: err=%v out=%q", err, out.String())
					}
					return
				}
				if err != nil {
					t.Fatal("valid fixture did not reach response decoding", err)
				}
				if jsonOutput {
					var result struct {
						Restored []int64 `json:"restored"`
					}
					if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Restored) != 2 || result.Restored[0] != 7 || result.Restored[1] != 8 {
						t.Fatalf("valid JSON changed: %q err=%v", out.String(), err)
					}
				} else if strings.TrimSpace(out.String()) != "Restored 2 item(s)" {
					t.Fatalf("valid text changed: %q", out.String())
				}
			})
		}
	}
}
