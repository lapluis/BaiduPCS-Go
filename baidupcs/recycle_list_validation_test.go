package baidupcs

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRecycleListRejectsInvalidMetadataAndDuplicates(t *testing.T) {
	const valid = `{"fs_id":7,"path":"/same","isdir":0,"size":3}`
	for _, tc := range []struct {
		name, list string
		count      int
		invalid    bool
	}{
		{"file", `[` + valid + `]`, 1, false},
		{"empty", `[]`, 0, false},
		{"directory", `[{"fs_id":8,"path":"/dir","isdir":1,"size":0}]`, 1, false},
		{"same-path-distinct-ids", `[` + valid + `,{"fs_id":8,"path":"/same","isdir":0,"size":4}]`, 2, false},
		{"negative-directory-flag", `[` + valid + `,{"fs_id":8,"isdir":-1}]`, 0, true},
		{"invalid-directory-flag", `[` + valid + `,{"fs_id":8,"isdir":2}]`, 0, true},
		{"negative-size", `[` + valid + `,{"fs_id":8,"size":-1}]`, 0, true},
		{"zero-id", `[` + valid + `,{"fs_id":0}]`, 0, true},
		{"negative-id", `[` + valid + `,{"fs_id":-1}]`, 0, true},
		{"duplicate-id", `[` + valid + `,{"fs_id":7,"path":"/other"}]`, 0, true},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/context", true: "/legacy"}[legacy], func(t *testing.T) {
				body := &locateBody{Reader: strings.NewReader(`{"errno":0,"list":` + tc.list + `}`)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var files RecycleFDInfoList
				var err pcserror.Error
				if legacy {
					files, err = pcs.RecycleList(1)
				} else {
					files, err = pcs.RecycleListWithContext(context.Background(), 1)
				}
				if !body.closed {
					t.Error("response body not closed")
				}
				if tc.invalid {
					if err == nil || files != nil || err.GetErrType() != pcserror.ErrTypeOthers {
						t.Fatalf("malformed list accepted: files=%v err=%v", files, err)
					}
				} else if err != nil || len(files) != tc.count {
					t.Fatalf("valid list changed: files=%v err=%v", files, err)
				}
			})
		}
	}
}
