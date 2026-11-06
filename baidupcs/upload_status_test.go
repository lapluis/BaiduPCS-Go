package baidupcs

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type uploadPreparation struct {
	name, operation string
	prepare         func(*BaiduPCS, UploadFunc) (io.ReadCloser, pcserror.Error)
}

func uploadPreparations() []uploadPreparation {
	return []uploadPreparation{
		{"file", OperationUpload, func(p *BaiduPCS, f UploadFunc) (io.ReadCloser, pcserror.Error) {
			return p.PrepareUpload(OverWritePolicy, "/fixture", f)
		}},
		{"part", OperationUploadTmpFile, func(p *BaiduPCS, f UploadFunc) (io.ReadCloser, pcserror.Error) { return p.PrepareUploadTmpFile(f) }},
		{"superfile2", OperationUploadSuperfile2, func(p *BaiduPCS, f UploadFunc) (io.ReadCloser, pcserror.Error) {
			return p.PrepareUploadSuperfile2("fixture", "/fixture", 0, 0, f)
		}},
	}
}

func TestUploadPreparationPreservesHTTPFailures(t *testing.T) {
	for _, prep := range uploadPreparations() {
		for _, tc := range []struct {
			name string
			code int
			body string
			kind pcserror.ErrType
		}{
			{"empty-object", 400, `{}`, pcserror.ErrTypeNetError},
			{"zero-error-code", 500, `{"error_code":0}`, pcserror.ErrTypeNetError},
			{"html", 503, `<html>fixture failure</html>`, pcserror.ErrTypeNetError},
			{"empty-body", 502, "", pcserror.ErrTypeNetError},
			{"remote-error", 403, `{"error_code":31066}`, pcserror.ErrTypeRemoteError},
			{"redirect", 307, `{}`, pcserror.ErrTypeNetError},
			{"success", 200, `{"fixture":"ok"}`, pcserror.ErrorTypeNoError},
		} {
			t.Run(prep.name+"/"+tc.name, func(t *testing.T) {
				defer func() {
					if err := recover(); err != nil {
						t.Errorf("HTTP response caused panic: %v", err)
					}
				}()
				body := &uploadDigestBody{Reader: strings.NewReader(tc.body)}
				reader, err := prep.prepare(&BaiduPCS{}, func(string, http.CookieJar) (*http.Response, error) {
					return &http.Response{StatusCode: tc.code, Header: make(http.Header), Body: body}, nil
				})
				if tc.kind == pcserror.ErrorTypeNoError {
					if err != nil || reader != body || body.closed {
						t.Fatalf("success body unavailable: reader=%v err=%v closed=%v", reader, err, body.closed)
					}
					reader.Close()
					return
				}
				if reader != nil || err == nil || err.GetErrType() != tc.kind || err.GetOperation() != prep.operation || !body.closed {
					t.Fatalf("failure lost: reader=%v err=%v closed=%v", reader, err, body.closed)
				}
				if tc.kind == pcserror.ErrTypeRemoteError {
					if err.GetRemoteErrCode() != 31066 {
						t.Fatalf("remote code lost: %v", err)
					}
				} else if !strings.Contains(err.Error(), fmt.Sprintf("HTTP status %d", tc.code)) {
					t.Fatalf("HTTP status lost: %v", err)
				}
				if tc.name == "html" || tc.name == "empty-body" {
					var parseErr *pcserror.PCSErrInfo
					if !errors.As(err.GetError(), &parseErr) || parseErr.GetErrType() != pcserror.ErrTypeJSONParseError {
						t.Fatalf("response parse cause lost: %v", err)
					}
				}
			})
		}
	}
}

func TestUploadPreparationRejectsIncompleteResponse(t *testing.T) {
	callbackErr := errors.New("fixture callback failure")
	for _, prep := range uploadPreparations() {
		for _, code := range []int{0, 200, 500, -1} {
			t.Run(fmt.Sprintf("%s/%d", prep.name, code), func(t *testing.T) {
				defer func() {
					if err := recover(); err != nil {
						t.Errorf("incomplete response caused panic: %v", err)
					}
				}()
				reader, err := prep.prepare(&BaiduPCS{}, func(string, http.CookieJar) (*http.Response, error) {
					if code == -1 {
						return &http.Response{StatusCode: 500}, callbackErr
					}
					if code == 0 {
						return nil, nil
					}
					return &http.Response{StatusCode: code}, nil
				})
				if reader != nil || err == nil || err.GetErrType() != pcserror.ErrTypeNetError || err.GetOperation() != prep.operation {
					t.Fatalf("incomplete response accepted: reader=%v err=%v", reader, err)
				}
				if code == -1 && !errors.Is(err, callbackErr) {
					t.Fatalf("callback error lost: %v", err)
				}
			})
		}
	}
}
