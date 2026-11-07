package pcsclone

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestRecycleCommandsDoNotReportMalformedMutationResults(t *testing.T) {
	for _, tc := range []struct {
		name, action, payload string
		ids                   []string
	}{
		{"restore-null", "restore", `{"error_code":0,"extra":{"list":[null]}}`, []string{"7"}},
		{"restore-mixed", "restore", `{"error_code":0,"extra":{"list":[{"fs_id":7},null]}}`, []string{"7", "8"}},
		{"clear-negative", "clear", `{"error_code":0,"extra":{"succNum":-1}}`, nil},
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", tc.name, jsonOutput), func(t *testing.T) {
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, tc.payload) }, "recycle/"+tc.action, append([]string{"bd:"}, tc.ids...), flags)
				if err := c.cmd.Run(c); err == nil || out.Len() != 0 {
					t.Fatalf("malformed mutation result reported success: err=%v output=%q", err, out.String())
				}
			})
		}
	}
}
