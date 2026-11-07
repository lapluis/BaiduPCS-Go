package pcsclone

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRecycleCommandRejectsPartialNullLists(t *testing.T) {
	const kind = "recycle"
	item := `{"shareId":7,"fs_id":7,"path":"/fixture","typicalPath":"/fixture","server_filename":"fixture"}`
	for _, jsonOutput := range []bool{false, true} {
		for _, tc := range []struct {
			name, list string
			count      int
			invalid    bool
		}{
			{"empty", `[]`, 0, false},
			{"valid", `[` + item + `]`, 1, false},
			{"null", `[null]`, 0, true},
			{"null-first", `[null,` + item + `]`, 0, true},
			{"null-last", `[` + item + `,null]`, 0, true},
		} {
			t.Run(fmt.Sprintf("%s/json=%t/%s", kind, jsonOutput, tc.name), func(t *testing.T) {
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"errno":0,"list":`+tc.list+`}`)
				}, kind+"/list", []string{"bd:"}, flags)
				err := c.cmd.Run(c)
				if tc.invalid {
					if err == nil || out.Len() != 0 {
						t.Fatalf("partial list reported success: err=%v output=%q", err, out.String())
					}
					return
				}
				if err != nil {
					t.Fatal("valid list failed", err)
				}
				if jsonOutput {
					var rows []json.RawMessage
					if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != tc.count {
						t.Fatal("valid JSON list changed", out.String(), err)
					}
				} else if tc.count == 0 && out.Len() != 0 || tc.count > 0 && !strings.Contains(out.String(), "fixture") {
					t.Fatal("valid text list changed", out.String())
				}
			})
		}
	}
}
