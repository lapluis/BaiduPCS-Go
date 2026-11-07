package baidupcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type apiStatusCancelBody struct {
	io.Reader
	cancel context.CancelFunc
	closes int
}

func (b *apiStatusCancelBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	b.cancel()
	return n, err
}
func (b *apiStatusCancelBody) Close() error { b.closes++; return nil }

func TestAPIStatusInspectionPreservesCancellationAndCause(t *testing.T) {
	for _, rt := range []reqType{reqTypePCS, reqTypePan} {
		for _, tc := range []struct {
			name, payload  string
			status, remote int
		}{
			{"pcs-error", `{"error_code":31066}`, 403, 31066},
			{"pan-error", `{"errno":-9}`, 403, -9},
			{"success-shaped", `{"error_code":0,"errno":0}`, 500, 0},
			{"html", `<html>fixture failure</html>`, 503, 0},
			{"canceled-headers", `{"error_code":0,"errno":0}`, 200, 0},
		} {
			t.Run(fmt.Sprintf("%d/%s", rt, tc.name), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				body := &apiStatusCancelBody{Reader: strings.NewReader(tc.payload), cancel: cancel}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					if tc.status == 200 {
						cancel()
					}
					resp := response(req, "")
					resp.StatusCode, resp.Body = tc.status, body
					return resp, nil
				})
				reader, err := pcs.sendReqReturnReadCloserContext(ctx, rt, "fixture status", http.MethodGet, "https://fixture.invalid/response", nil, nil)
				if reader != nil {
					reader.Close()
				}
				if !errors.Is(err, context.Canceled) || err.GetErrType() != pcserror.ErrTypeNetError || reader != nil || calls != 1 || body.closes != 1 {
					t.Errorf("status inspection lost cancellation or ownership: reader=%t calls=%d closes=%d err=%v", reader != nil, calls, body.closes, err)
				}
				if tc.remote != 0 && err != nil {
					var original pcserror.Error
					if !errors.As(err.GetError(), &original) || original.GetRemoteErrCode() != tc.remote {
						t.Errorf("remote cause lost during cancellation: %v", err)
					}
				} else if tc.status != 200 && err != nil && !strings.Contains(err.Error(), fmt.Sprintf("HTTP status %d", tc.status)) {
					t.Errorf("HTTP failure cause lost during cancellation: %v", err)
				}
			})
		}
	}
}
