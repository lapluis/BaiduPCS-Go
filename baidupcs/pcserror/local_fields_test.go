package pcserror

import (
	"fmt"
	"strings"
	"testing"
)

func TestResponseCannotOverrideLocalErrorFields(t *testing.T) {
	for _, tc := range []struct {
		name, success, failure string
		code                   int
		newInfo                func(string) Error
	}{
		{"PCS", `"error_code":0`, `"error_code":31066`, 31066, func(op string) Error { return NewPCSErrorInfo(op) }},
		{"Pan", `"errno":0`, `"errno":-9`, -9, func(op string) Error { return NewPanErrorInfo(op) }},
		{"XPan", `"errno":0,"return_type":2`, `"errno":7,"return_type":2`, 7, func(op string) Error { return NewXPanErrorInfo(op) }},
		{"Dlink", `"errno":0`, `"errno":7,"msg":"fixture remote failure"`, 7, func(op string) Error { return NewDlinkErrInfo(op) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, reserved := range []string{
				`"Operation":""`,
				`"operation":"spoofed operation"`,
				`"Operation":{"unexpected":"object"}`,
				`"ErrType":999,"Err":null`,
				`"errtype":"unexpected","err":"spoofed cause"`,
			} {
				for _, failure := range []bool{false, true} {
					info := tc.newInfo("locally owned operation")
					fields := tc.success
					if failure {
						fields = tc.failure
					}
					err := HandleJSONParse("locally owned operation", strings.NewReader(fmt.Sprintf("{%s,%s}", fields, reserved)), info)
					if info.GetOperation() != "locally owned operation" || info.GetError() != nil {
						t.Errorf("local context changed by %s: op=%q cause=%v", reserved, info.GetOperation(), info.GetError())
					}
					if !failure {
						if err != nil || info.GetErrType() != ErrorTypeNoError {
							t.Errorf("unrelated fields corrupted success: %s: %v type=%d", reserved, err, info.GetErrType())
						}
						continue
					}
					if err == nil || err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code || !strings.Contains(err.Error(), "locally owned operation") || strings.Contains(err.Error(), StrSuccess) {
						t.Errorf("remote failure lost its local diagnostic context: %s: %v", reserved, err)
					}
				}
			}
		})
	}
}
