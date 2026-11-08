package baidupcs

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestShareResultsRejectUnusableIdentities(t *testing.T) {
	for _, tc := range []struct {
		name, action, payload string
		count                 int
		id                    int64
		invalid               bool
	}{
		{"create-valid", "create", `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":7}`, 1, 7, false},
		{"create-large", "create", `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":9223372036854775807}`, 1, 9223372036854775807, false},
		{"create-zero", "create", `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":0}`, 0, 0, true},
		{"create-negative", "create", `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":-1}`, 0, 0, true},
		{"create-missing", "create", `{"errno":0,"link":"https://fixture.invalid/s/one"}`, 0, 0, true},
		{"create-null", "create", `{"errno":0,"link":"https://fixture.invalid/s/one","shareid":null}`, 0, 0, true},
		{"list-empty", "list", `{"errno":0,"list":[]}`, 0, 0, false},
		{"list-valid", "list", `{"errno":0,"list":[{"shareId":7}]}`, 1, 7, false},
		{"list-same-path-distinct-ids", "list", `{"errno":0,"list":[{"shareId":7,"typicalPath":"/same"},{"shareId":8,"typicalPath":"/same"}]}`, 2, 7, false},
		{"list-zero", "list", `{"errno":0,"list":[{"shareId":7},{"shareId":0}]}`, 0, 0, true},
		{"list-negative", "list", `{"errno":0,"list":[{"shareId":7},{"shareId":-1}]}`, 0, 0, true},
		{"list-missing", "list", `{"errno":0,"list":[{"shareId":7},{}]}`, 0, 0, true},
		{"list-duplicate", "list", `{"errno":0,"list":[{"shareId":7},{"shareId":7}]}`, 0, 0, true},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/context", true: "/legacy"}[legacy], func(t *testing.T) {
				body := &shareTrackedBody{Reader: strings.NewReader(tc.payload)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var err pcserror.Error
				var id int64
				count := 0
				if tc.action == "create" {
					var shared *Shared
					if legacy {
						shared, err = pcs.ShareSet([]string{"/one"}, &ShareOption{Password: "Ab12"})
					} else {
						shared, err = pcs.ShareSetWithContext(context.Background(), []string{"/one"}, &ShareOption{Password: "Ab12"})
					}
					if shared != nil {
						count, id = 1, shared.ShareID
					}
				} else {
					var records ShareRecordInfoList
					if legacy {
						records, err = pcs.ShareList(1)
					} else {
						records, err = pcs.ShareListWithContext(context.Background(), 1)
					}
					count = len(records)
					if count > 0 {
						id = records[0].ShareID
					}
					if tc.invalid && records != nil {
						t.Errorf("partial list returned: %v", records)
					}
				}
				if !body.closed {
					t.Error("response body not closed")
				}
				if tc.invalid {
					if err == nil || count != 0 || err.GetErrType() != pcserror.ErrTypeOthers {
						t.Fatalf("unusable identity accepted: count=%d id=%d err=%v", count, id, err)
					}
				} else if err != nil || count != tc.count || id != tc.id {
					t.Fatalf("valid share changed: count=%d id=%d err=%v", count, id, err)
				}
			})
		}
	}
}
