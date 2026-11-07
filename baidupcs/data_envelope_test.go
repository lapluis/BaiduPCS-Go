package baidupcs

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestDataResponsesRejectAlternateErrorEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, fields, alternate, native string
		code                            int
		call                            func(*BaiduPCS) pcserror.Error
	}{
		{"quota", `"quota":100,"used":25`, `"errno":-9`, `"error_code":0`, -9, func(p *BaiduPCS) pcserror.Error {
			quota, used, err := p.QuotaInfo()
			if err != nil && (quota != 0 || used != 0) {
				t.Errorf("failed quota leaked data: %d %d", quota, used)
			}
			return err
		}},
		{"metadata", `"list":[{"path":"/one","fs_id":7,"size":3}]`, `"errno":-9`, `"error_code":0`, -9, func(p *BaiduPCS) pcserror.Error {
			info, err := p.FilesDirectoriesMeta("/one")
			if err != nil && info != nil {
				t.Errorf("failed metadata leaked data: %v", info)
			}
			return err
		}},
		{"directory-list", `"list":[]`, `"error_code":31066`, `"errno":0`, 31066, func(p *BaiduPCS) pcserror.Error {
			list, err := p.FilesDirectoriesList("/one", nil)
			if err != nil && list != nil {
				t.Errorf("failed listing leaked data: %v", list)
			}
			return err
		}},
		{"recycle-list", `"list":[]`, `"error_code":31066`, `"errno":0`, 31066, func(p *BaiduPCS) pcserror.Error {
			list, err := p.RecycleList(1)
			if err != nil && list != nil {
				t.Errorf("failed recycle listing leaked data: %v", list)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, explicitZero := range []bool{false, true} {
				payload := fmt.Sprintf("{%s,%s}", tc.fields, tc.alternate)
				if explicitZero {
					payload = fmt.Sprintf("{%s,%s,%s}", tc.fields, tc.native, tc.alternate)
				}
				body := &mutationEnvelopeBody{Reader: strings.NewReader(payload)}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				err := tc.call(pcs)
				if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code {
					t.Errorf("alternate data-response error ignored: %s: %v", payload, err)
				}
				if calls != 1 || body.closes != 1 {
					t.Errorf("response ownership changed: calls=%d closes=%d", calls, body.closes)
				}
			}
		})
	}
}
