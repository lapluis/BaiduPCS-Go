package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRecycleRestoreValidatesResultIdentities(t *testing.T) {
	for _, tc := range []struct {
		name, list string
		want       []int64
		invalid    bool
	}{
		{"valid", `[{"fs_id":7},{"fs_id":8}]`, []int64{7, 8}, false},
		{"empty", `[]`, []int64{}, false},
		{"zero", `[{"fs_id":7},{"fs_id":0}]`, []int64{7}, true},
		{"negative", `[{"fs_id":7},{"fs_id":-1}]`, []int64{7}, true},
		{"missing", `[{"fs_id":7},{}]`, []int64{7}, true},
		{"duplicate", `[{"fs_id":7},{"fs_id":7}]`, []int64{7}, true},
		{"mixed", `[{"fs_id":7},null,{"fs_id":0},{"fs_id":-1},{"fs_id":7},{"fs_id":8}]`, []int64{7, 8}, true},
	} {
		for _, code := range []int{0, 31066} {
			for _, legacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/code=%d/legacy=%t", tc.name, code, legacy), func(t *testing.T) {
					body := &shareTrackedBody{Reader: strings.NewReader(fmt.Sprintf(`{"error_code":%d,"extra":{"list":%s}}`, code, tc.list))}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					var items []*FsIDJSON
					var err pcserror.Error
					if legacy {
						items, err = pcs.RecycleRestore(7, 8)
					} else {
						items, err = pcs.RecycleRestoreWithContext(context.Background(), 7, 8)
					}
					if !body.closed {
						t.Error("response not closed")
					}
					if code != 0 {
						if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != code {
							t.Fatalf("remote error lost: %v", err)
						}
					} else if tc.invalid {
						if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || items != nil {
							t.Fatalf("invalid success accepted: items=%v err=%v", items, err)
						}
					} else if err != nil {
						t.Fatal(err)
					}
					want := tc.want
					if code == 0 && tc.invalid {
						want = []int64{}
					}
					got := make([]int64, 0, len(items))
					for _, item := range items {
						if item == nil {
							t.Fatal("null partial item")
						}
						got = append(got, item.FsID)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("wrong restored IDs: got=%v want=%v", got, want)
					}
				})
			}
		}
	}
}
