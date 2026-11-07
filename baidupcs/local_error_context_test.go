package baidupcs

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestDataResponseCannotReplaceLocalErrorContext(t *testing.T) {
	for _, tc := range []struct {
		name, operation, fields string
		code                    int
		call                    func(*BaiduPCS) pcserror.Error
	}{
		{"quota", OperationQuotaInfo, `"error_code":31066,"quota":100,"used":25`, 31066, func(p *BaiduPCS) pcserror.Error {
			quota, used, err := p.QuotaInfo()
			if quota != 0 || used != 0 {
				t.Errorf("failed quota leaked values: %d %d", quota, used)
			}
			return err
		}},
		{"directory", OperationFilesDirectoriesList, `"errno":-9,"list":[]`, -9, func(p *BaiduPCS) pcserror.Error {
			list, err := p.FilesDirectoriesList("/one", nil)
			if list != nil {
				t.Errorf("failed listing leaked values: %v", list)
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reserved := range []string{`"Operation":""`, `"operation":"spoofed operation"`, `"ErrType":999,"Err":null`, `"operation":{},"errtype":"unexpected","err":"spoofed cause"`} {
				body := &mutationEnvelopeBody{Reader: strings.NewReader(fmt.Sprintf("{%s,%s}", tc.fields, reserved))}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				err := tc.call(pcs)
				if err == nil || err.GetOperation() != tc.operation || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code || !strings.Contains(err.Error(), tc.operation) || strings.Contains(err.Error(), pcserror.StrSuccess) {
					t.Errorf("remote response replaced local context: %s: %v", reserved, err)
				}
				if calls != 1 || body.closes != 1 {
					t.Errorf("response ownership changed: calls=%d closes=%d", calls, body.closes)
				}
			}
		})
	}
}
