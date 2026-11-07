package panhome

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

const validSignaturePage = `{"sign1":"fixture","sign3":"key","timestamp":123,}`

type signatureTransport func(*http.Request) (*http.Response, error)

func (f signatureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type signatureBody struct {
	*strings.Reader
	closed bool
	reads  int
}

func (b *signatureBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }

func (b *signatureBody) Close() error { b.closed = true; return nil }

func TestSignatureRejectsUnsuccessfulPageStatus(t *testing.T) {
	for _, code := range []int{200, 204, 301, 304, 401, 403, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			body := &signatureBody{Reader: strings.NewReader(validSignaturePage)}
			client := requester.NewHTTPClient()
			client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: body, Request: r}, nil
			})
			ph := NewPanHome(client)
			sign, err := ph.Signature()
			if code == http.StatusOK {
				if err != nil || sign == nil || sign.Timestamp() != "123" || sign.Sign() == "" {
					t.Fatal("valid page rejected", sign, err)
				}
			} else if err == nil || sign != nil || !strings.Contains(err.Error(), fmt.Sprint(code)) {
				t.Fatalf("HTTP error page accepted: sign=%v err=%v", sign, err)
			}
			if code != http.StatusOK && body.reads != 0 {
				t.Fatal("error page was read as signature data")
			}
			if !body.closed {
				t.Fatal("signature response not closed")
			}
		})
	}
}

func TestFailedSignaturePageIsNotCached(t *testing.T) {
	client := requester.NewHTTPClient()
	calls := 0
	client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		code := http.StatusOK
		if calls == 1 {
			code = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: &signatureBody{Reader: strings.NewReader(validSignaturePage)}, Request: r}, nil
	})
	ph := NewPanHome(client)
	if sign, err := ph.CacheSignature(); err == nil || sign != nil {
		t.Error("error response cached as valid signature", sign, err)
	}
	if sign, err := ph.CacheSignature(); err != nil || sign == nil || calls != 2 {
		t.Fatal("valid retry failed or error signature reused", sign, err, calls)
	}
	if sign, err := ph.CacheSignature(); err != nil || sign == nil || calls != 2 {
		t.Fatal("valid signature was not cached", sign, err, calls)
	}
}

func TestSignatureRedirectErrorsKeepClientPolicy(t *testing.T) {
	for _, location := range []string{"/", "https://passport.baidu.com/login", "https://other.fixture.invalid/"} {
		t.Run(location, func(t *testing.T) {
			client := requester.NewHTTPClient()
			callerError := errors.New("caller redirect policy")
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return callerError }
			body := &signatureBody{Reader: strings.NewReader(validSignaturePage)}
			calls := 0
			client.Client.Transport = signatureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{location}}, Body: body, Request: r}, nil
			})
			_, err := NewPanHome(client).Signature()
			want := ErrCookieInvalid
			if strings.Contains(location, "other.fixture") {
				want = ErrUnknownLocation
			}
			if !errors.Is(err, want) || calls != 1 || !body.closed || !errors.Is(client.CheckRedirect(nil, nil), callerError) {
				t.Fatal("redirect behavior or caller policy changed", err, calls, body.closed)
			}
		})
	}
}
