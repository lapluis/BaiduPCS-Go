package pcserror

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestEmbeddedErrorEnvelopePreservesDataAndDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, success, alternate, native, invalid string
		alternateCode, nativeCode                 int
	}{
		{"PCS", `"error_code":0,"errno":0`, `"error_code":0,"errno":-9`, `"error_code":31066,"errno":-9`, `"errno":"invalid"`, -9, 31066},
		{"Pan", `"errno":0,"error_code":0`, `"errno":0,"error_code":31066,"error_msg":"fixture"`, `"errno":-9,"error_code":31066`, `"error_code":true`, 31066, -9},
		{"XPan", `"errno":0,"return_type":2,"error_code":0`, `"errno":0,"return_type":2,"error_code":31066,"error_msg":"fixture"`, `"errno":7,"return_type":2,"error_code":31066`, `"return_type":2,"error_code":[]`, 31066, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, phase := range []string{"success", "alternate", "native", "invalid"} {
				t.Run(phase, func(t *testing.T) {
					var info interface{}
					var value *int
					var list *[]int
					switch tc.name {
					case "PCS":
						p := &struct {
							*PCSErrInfo
							Value int
							List  []int
						}{PCSErrInfo: NewPCSErrorInfo("fixture data")}
						info, value, list = p, &p.Value, &p.List
					case "Pan":
						p := &struct {
							*PanErrorInfo
							Value int
							List  []int
						}{PanErrorInfo: NewPanErrorInfo("fixture data")}
						info, value, list = p, &p.Value, &p.List
					case "XPan":
						p := &struct {
							*XPanErrorInfo
							Value int
							List  []int
						}{XPanErrorInfo: NewXPanErrorInfo("fixture data")}
						info, value, list = p, &p.Value, &p.List
					}
					fields := map[string]string{"success": tc.success, "alternate": tc.alternate, "native": tc.native, "invalid": tc.invalid}[phase]
					body := fmt.Sprintf(`{%s,"value":7,"list":[1,2]}`, fields)
					err := HandleJSONParse("fixture data", strings.NewReader(body), info)
					if phase == "success" {
						if err != nil || *value != 7 || len(*list) != 2 || (*list)[0] != 1 || (*list)[1] != 2 {
							t.Fatalf("embedding swallowed data fields: value=%d list=%v err=%v", *value, *list, err)
						}
						return
					}
					if phase == "invalid" {
						var typeErr *json.UnmarshalTypeError
						if err == nil || err.GetErrType() != ErrTypeJSONParseError || !errors.As(err, &typeErr) {
							t.Fatalf("parse cause lost: %v", err)
						}
						return
					}
					code := tc.alternateCode
					if phase == "native" {
						code = tc.nativeCode
					}
					if err == nil || err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() != code || err.GetOperation() != "fixture data" {
						t.Fatalf("embedded remote error lost: %s: %v", body, err)
					}
					if err.GetRemoteErrMsg() == "" || !strings.Contains(err.Error(), fmt.Sprintf("代码: %d", code)) || strings.Contains(err.Error(), StrSuccess) {
						t.Fatalf("embedded error rendered as success or wrong code: %v", err)
					}
				})
			}
		})
	}
}
