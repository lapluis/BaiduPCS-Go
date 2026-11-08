package pcsclone

import (
	"fmt"
	"io"
	"net/http"
	"testing"
)

func TestRecycleListDoesNotPublishInvalidMetadata(t *testing.T) {
	for _, item := range []string{`{"fs_id":8,"isdir":-1}`, `{"fs_id":8,"isdir":2}`, `{"fs_id":8,"size":-1}`, `{"fs_id":0}`, `{"fs_id":-1}`, `{"fs_id":7}`} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", item, jsonOutput), func(t *testing.T) {
				var flags []string
				if jsonOutput {
					flags = []string{"--json"}
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, `{"errno":0,"list":[{"fs_id":7,"path":"/valid","isdir":0,"size":3},`+item+`]}`)
				}, "recycle/list", []string{"bd:"}, flags)
				if err := c.cmd.Run(c); err == nil || out.Len() != 0 {
					t.Fatalf("invalid list published: err=%v output=%q", err, out.String())
				}
			})
		}
	}
}
