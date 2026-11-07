package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func queryPublicList(pcs *BaiduPCS, kind string, contextual bool) (int, bool, pcserror.Error) {
	if kind == "share" {
		var list ShareRecordInfoList
		var err pcserror.Error
		if contextual {
			list, err = pcs.ShareListWithContext(context.Background(), 1)
		} else {
			list, err = pcs.ShareList(1)
		}
		return len(list), list == nil, err
	}
	var list RecycleFDInfoList
	var err pcserror.Error
	if contextual {
		list, err = pcs.RecycleListWithContext(context.Background(), 1)
	} else {
		list, err = pcs.RecycleList(1)
	}
	return len(list), list == nil, err
}

func TestPublicListsRejectNullEntries(t *testing.T) {
	item := `{"shareId":7,"fs_id":7,"path":"/fixture"}`
	for _, kind := range []string{"share", "recycle"} {
		for _, contextual := range []bool{false, true} {
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
				t.Run(fmt.Sprintf("%s/context=%t/%s", kind, contextual, tc.name), func(t *testing.T) {
					body := &shareTrackedBody{Reader: strings.NewReader(`{"errno":0,"list":` + tc.list + `}`)}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					count, isNil, err := queryPublicList(pcs, kind, contextual)
					if tc.invalid {
						if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || !strings.Contains(err.Error(), "null entry") || !isNil || count != 0 {
							t.Fatalf("invalid list accepted: count=%d nil=%v err=%v", count, isNil, err)
						}
					} else if err != nil || count != tc.count {
						t.Fatal("valid list rejected", count, tc.count, err)
					}
					if !body.closed {
						t.Fatal("list response not closed")
					}
				})
			}
		}
	}
}

func TestPublicListRemoteErrorPrecedesEntryValidation(t *testing.T) {
	for _, kind := range []string{"share", "recycle"} {
		pcs := fixturePCS(func(r *http.Request) (*http.Response, error) { return response(r, `{"errno":110,"list":[null]}`), nil })
		count, isNil, err := queryPublicList(pcs, kind, true)
		if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != 110 || count != 0 || !isNil {
			t.Fatal("remote error lost", kind, count, isNil, err)
		}
	}
}
