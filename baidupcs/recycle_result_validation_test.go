package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRecycleRestoreValidatesSuccessfulItemsAndRetainsPartialErrors(t *testing.T) {
	for _, tc := range []struct {
		name, list string
		code       int
		want       []int64
		invalid    bool
	}{
		{"valid", `[{"fs_id":7},{"fs_id":8}]`, 0, []int64{7, 8}, false},
		{"empty", `[]`, 0, nil, false},
		{"null", `[null]`, 0, nil, true},
		{"null-first", `[null,{"fs_id":7}]`, 0, nil, true},
		{"null-last", `[{"fs_id":7},null]`, 0, nil, true},
		{"partial-error", `[{"fs_id":7},null,{"fs_id":8}]`, 31066, []int64{7, 8}, false},
		{"null-error", `[null]`, 31066, nil, false},
		{"valid-error", `[{"fs_id":7}]`, 31066, []int64{7}, false},
	} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/legacy=%v", tc.name, legacy), func(t *testing.T) {
				body := &shareTrackedBody{Reader: strings.NewReader(fmt.Sprintf(`{"error_code":%d,"extra":{"list":%s}}`, tc.code, tc.list))}
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
				if tc.code != 0 {
					if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code {
						t.Fatal("remote restore error lost", err)
					}
				} else if tc.invalid {
					if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || items != nil {
						t.Fatal("invalid successful restore accepted", items, err)
					}
				} else if err != nil {
					t.Fatal("valid restore failed", err)
				}
				if len(items) != len(tc.want) {
					t.Fatalf("restored IDs count=%d want=%d", len(items), len(tc.want))
				}
				for i, item := range items {
					if item == nil || item.FsID != tc.want[i] {
						t.Fatal("invalid or lost partial restore item", items)
					}
				}
				if !body.closed {
					t.Fatal("restore response not closed")
				}
			})
		}
	}
}

func TestRecycleClearRejectsNegativeSuccessCount(t *testing.T) {
	for _, code := range []int{0, 31066} {
		for _, count := range []int{-1, 0, 3} {
			for _, legacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("code=%d/count=%d/legacy=%v", code, count, legacy), func(t *testing.T) {
					body := &shareTrackedBody{Reader: strings.NewReader(fmt.Sprintf(`{"error_code":%d,"extra":{"succNum":%d}}`, code, count))}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					var got int
					var err pcserror.Error
					if legacy {
						got, err = pcs.RecycleClear()
					} else {
						got, err = pcs.RecycleClearWithContext(context.Background())
					}
					if code != 0 {
						if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != code {
							t.Fatal("clear remote error lost", err)
						}
					} else if count < 0 {
						if err == nil || err.GetErrType() != pcserror.ErrTypeOthers {
							t.Fatal("negative count reported success", got, err)
						}
					} else if err != nil {
						t.Fatal("valid clear failed", err)
					}
					if got != max(count, 0) || !body.closed {
						t.Fatal("clear count or response lifecycle incorrect", got, count, body.closed)
					}
				})
			}
		}
	}
}
