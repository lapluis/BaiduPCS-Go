package pcserror

import (
	"io"
	"strings"
	"testing"
)

func TestAPIErrorDecodersRequireObjectResponse(t *testing.T) {
	for name, decode := range map[string]func(string, io.Reader) Error{"PCS": DecodePCSJSONError, "Pan": DecodePanJSONError, "XPan": DecodeXPanJSONError} {
		t.Run(name, func(t *testing.T) {
			for _, body := range []string{"null", " \r\n\tnull ", "[]", "true", `"ok"`, "0", "", "{} null"} {
				err := decode("synthetic operation", strings.NewReader(body))
				if err == nil || err.GetErrType() != ErrTypeJSONParseError || err.GetOperation() != "synthetic operation" || err.GetError() == nil {
					t.Errorf("response %q did not return a JSON parse error: %v", body, err)
				}
			}
		})
	}
}

func TestAPIErrorDecodersKeepSuccessAndRemoteErrors(t *testing.T) {
	for _, tc := range []struct {
		name             string
		decode           func(string, io.Reader) Error
		success, failure string
		code             int
	}{
		{"PCS", DecodePCSJSONError, `{"error_code":0}`, `{"error_code":31066}`, 31066},
		{"Pan", DecodePanJSONError, `{"errno":0}`, `{"errno":-9}`, -9},
		{"XPan", DecodeXPanJSONError, `{"errno":0,"return_type":2}`, `{"errno":1,"return_type":0}`, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.decode("synthetic", strings.NewReader(" \n"+tc.success+"\r\n")); err != nil {
				t.Fatalf("valid success rejected: %v", err)
			}
			err := tc.decode("synthetic", strings.NewReader(tc.failure))
			if err == nil || err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code {
				t.Fatalf("remote error changed: %v", err)
			}
		})
	}
}
