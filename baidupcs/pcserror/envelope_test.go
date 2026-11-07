package pcserror

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestErrorDecodersRejectAlternateErrorEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func(string, io.Reader) Error
		bodies []string
		code   int
	}{
		{"PCS", DecodePCSJSONError, []string{`{"errno":-9}`, `{"error_code":0,"errno":-9}`}, -9},
		{"Pan", DecodePanJSONError, []string{`{"error_code":31066,"error_msg":"fixture error"}`, `{"errno":0,"error_code":31066}`}, 31066},
		{"XPan", DecodeXPanJSONError, []string{`{"error_code":31066}`, `{"errno":0,"return_type":2,"error_code":31066}`}, 31066},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, body := range tc.bodies {
				err := tc.decode("fixture mutation", strings.NewReader(body))
				if err == nil || err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code || err.GetOperation() != "fixture mutation" {
					t.Errorf("explicit alternate error lost for %s: %v", body, err)
				}
				if strings.Contains(body, "fixture error") {
					alternate, ok := err.(*PCSErrInfo)
					if !ok || alternate.ErrMsg != "fixture error" {
						t.Errorf("alternate PCS message lost: %v", err)
					}
				}
			}
		})
	}
}

func TestErrorDecoderEnvelopePrecedenceAndControls(t *testing.T) {
	for _, tc := range []struct {
		name    string
		decode  func(string, io.Reader) Error
		failure string
		code    int
		success []string
		invalid string
	}{
		{"PCS", DecodePCSJSONError, `{"error_code":31066,"errno":-9}`, 31066, []string{`{}`, `{"errno":0}`, `{"error_code":0,"errno":0}`}, `{"errno":"invalid"}`},
		{"Pan", DecodePanJSONError, `{"errno":-9,"error_code":31066}`, -9, []string{`{}`, `{"error_code":0}`, `{"error_code":0,"errno":0}`}, `{"error_code":true}`},
		{"XPan", DecodeXPanJSONError, `{"errno":7,"return_type":2,"error_code":31066}`, 7, []string{`{"return_type":2}`, `{"errno":0,"return_type":2,"error_code":0}`}, `{"return_type":2,"error_code":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode("fixture", strings.NewReader(tc.failure))
			if err == nil || err.GetErrType() != ErrTypeRemoteError || err.GetRemoteErrCode() != tc.code {
				t.Fatalf("native error precedence changed: %v", err)
			}
			switch tc.name {
			case "PCS":
				if _, ok := err.(*PCSErrInfo); !ok {
					t.Fatalf("native PCS error type changed: %T", err)
				}
			case "Pan":
				if _, ok := err.(*PanErrorInfo); !ok {
					t.Fatalf("native Pan error type changed: %T", err)
				}
			case "XPan":
				if _, ok := err.(*XPanErrorInfo); !ok {
					t.Fatalf("native XPan error type changed: %T", err)
				}
			}
			for _, body := range tc.success {
				if err := tc.decode("fixture", strings.NewReader(body)); err != nil {
					t.Errorf("normal response rejected: %s: %v", body, err)
				}
			}
			err = tc.decode("fixture", strings.NewReader(tc.invalid))
			var typeErr *json.UnmarshalTypeError
			if err == nil || err.GetErrType() != ErrTypeJSONParseError || !errors.As(err, &typeErr) {
				t.Errorf("malformed error field lost its parse cause: %v", err)
			}
		})
	}
}
