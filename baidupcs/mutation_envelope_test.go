package baidupcs

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type mutationEnvelopeBody struct {
	io.Reader
	closes int
}

func (b *mutationEnvelopeBody) Close() error { b.closes++; return nil }

func TestMutationsRejectAlternateErrorEnvelopeWithHTTP200(t *testing.T) {
	for name, mutate := range map[string]func(*BaiduPCS) pcserror.Error{
		"copy":   func(p *BaiduPCS) pcserror.Error { return p.Copy(&CpMvJSON{From: "/one", To: "/two"}) },
		"move":   func(p *BaiduPCS) pcserror.Error { return p.Move(&CpMvJSON{From: "/one", To: "/two"}) },
		"rename": func(p *BaiduPCS) pcserror.Error { return p.Rename("/one", "/two") },
		"remove": func(p *BaiduPCS) pcserror.Error { return p.Remove("/one") },
		"mkdir":  func(p *BaiduPCS) pcserror.Error { return p.Mkdir("/one") },
		"merge": func(p *BaiduPCS) pcserror.Error {
			return p.UploadCreateSuperFileWithContext(context.Background(), "fixture-session", OverWritePolicy, 8, "/one", map[int]string{0: strings.Repeat("a", 32)})
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, explicitZero := range []bool{false, true} {
				payload, code := `{"errno":-9}`, -9
				if explicitZero {
					payload = `{"error_code":0,"errno":-9}`
				}
				if name == "merge" {
					payload, code = `{"error_code":31066}`, 31066
					if explicitZero {
						payload = `{"errno":0,"error_code":31066}`
					}
				}
				body := &mutationEnvelopeBody{Reader: strings.NewReader(payload)}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				err := mutate(pcs)
				if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != code {
					t.Errorf("HTTP 200 alternate error reported success or lost code: %s: %v", payload, err)
				}
				if calls != 1 || body.closes != 1 {
					t.Errorf("response ownership changed: calls=%d closes=%d", calls, body.closes)
				}
			}
		})
	}
}
