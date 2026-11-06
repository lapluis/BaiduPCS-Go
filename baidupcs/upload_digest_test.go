package baidupcs

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type uploadDigestBody struct {
	*strings.Reader
	closed bool
}

func (b *uploadDigestBody) Close() error { b.closed = true; return nil }

func TestUploadTmpFileRejectsMalformedDigest(t *testing.T) {
	for _, tc := range []struct {
		name, digest string
		valid        bool
	}{
		{"lowercase", EmptyContentMD5, true},
		{"uppercase", strings.ToUpper(EmptyContentMD5), true},
		{"missing", "", false},
		{"short", "abc", false},
		{"long", EmptyContentMD5 + "0", false},
		{"non-hex", strings.Repeat("z", 32), false},
		{"whitespace", strings.Repeat(" ", 32), false},
		{"leading-space", " " + EmptyContentMD5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(map[string]string{"md5": tc.digest})
			if err != nil {
				t.Fatal(err)
			}
			body := &uploadDigestBody{Reader: strings.NewReader(string(data))}
			pcs := &BaiduPCS{}
			digest, pcsErr := pcs.UploadTmpFile("fixture", "/fixture", 0, 0, func(_ string, _ http.CookieJar) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{}, Body: body}, nil
			})
			if !body.closed {
				t.Error("upload response body was not closed")
			}
			if tc.valid {
				if pcsErr != nil || digest != tc.digest {
					t.Fatalf("valid digest rejected: digest=%q err=%v", digest, pcsErr)
				}
			} else {
				if pcsErr == nil || pcsErr.GetErrType() != pcserror.ErrTypeInternalError || digest != "" {
					t.Fatalf("malformed digest accepted for checkpointing: digest=%q err=%v", digest, pcsErr)
				}
				want := ErrUploadMD5Invalid
				if tc.digest == "" {
					want = ErrUploadMD5NotFound
				}
				if !errors.Is(pcsErr.GetError(), want) {
					t.Fatalf("wrong digest failure classification: got=%v want=%v", pcsErr.GetError(), want)
				}
			}
		})
	}
}
