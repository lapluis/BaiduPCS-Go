package requester

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
)

type fetchResponseTransport func(*http.Request) (*http.Response, error)

func (f fetchResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fetchResponseBody struct {
	data   string
	err    error
	closes int
}

func (b *fetchResponseBody) Read(p []byte) (int, error) {
	n := copy(p, b.data)
	b.data = b.data[n:]
	if len(b.data) == 0 && b.err != nil {
		return n, b.err
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}
func (b *fetchResponseBody) Close() error { b.closes++; return nil }

func TestFetchResponseReadFailureAndClosure(t *testing.T) {
	for _, helper := range []string{"client", "default-fetch", "get"} {
		for _, readFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/read-fails=%t", helper, readFails), func(t *testing.T) {
				cause := errors.New("fixture response read failure")
				body := &fetchResponseBody{data: "received bytes"}
				if readFails {
					body.err = cause
				}
				client := NewHTTPClient()
				client.Transport = fetchResponseTransport(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: req}, nil
				})
				previous := DefaultClient
				DefaultClient = client
				defer func() { DefaultClient = previous }()
				var data []byte
				var err error
				switch helper {
				case "get":
					data, err = HTTPGet("https://fixture.invalid/response")
				case "default-fetch":
					data, err = Fetch(http.MethodGet, "https://fixture.invalid/response", nil, nil)
				default:
					data, err = client.Fetch(http.MethodGet, "https://fixture.invalid/response", nil, nil)
				}
				if string(data) != "received bytes" || body.closes != 1 || (err != nil) != readFails || readFails && !errors.Is(err, cause) {
					t.Fatalf("response data/error/ownership lost: data=%q closes=%d err=%v", data, body.closes, err)
				}
			})
		}
	}
}

func TestRequestRedirectPolicyAndResponseOwnershipSurviveClone(t *testing.T) {
	for _, policy := range []string{"follow", "reject", "return-response"} {
		for _, cloned := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/clone=%t", policy, cloned), func(t *testing.T) {
				cause := errors.New("fixture redirect rejected")
				redirect := &fetchResponseBody{data: "redirect body"}
				final := &fetchResponseBody{data: "final body"}
				calls, policyCalls := 0, 0
				client := NewHTTPClient()
				client.Transport = fetchResponseTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: final, Request: req}
					if req.URL.Path == "/start" {
						resp.StatusCode, resp.Body = 302, redirect
						resp.Header.Set("Location", "/final")
					} else if req.URL.Path != "/final" {
						t.Errorf("unexpected path: %s", req.URL.Path)
					}
					return resp, nil
				})
				client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
					policyCalls++
					if req.URL.Path != "/final" || len(via) != 1 {
						t.Error("redirect policy received wrong request history")
					}
					switch policy {
					case "reject":
						return cause
					case "return-response":
						return http.ErrUseLastResponse
					}
					return nil
				}
				if cloned {
					client = client.Clone()
				}
				resp, err := client.ReqWithContext(context.Background(), http.MethodGet, "https://fixture.invalid/start", nil, nil)
				if policyCalls != 1 {
					t.Errorf("redirect policy lost or repeated: %d", policyCalls)
				}
				if policy == "reject" {
					if !errors.Is(err, cause) || calls != 1 || redirect.closes != 1 || final.closes != 0 {
						t.Fatalf("rejected redirect followed or body not released: calls=%d closes=%d/%d err=%v", calls, redirect.closes, final.closes, err)
					}
					return
				}
				if err != nil || resp == nil {
					t.Fatalf("redirect response lost: %v", err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				want := "final body"
				if policy == "return-response" {
					want = "redirect body"
					if calls != 1 || redirect.closes != 0 || final.closes != 0 {
						t.Error("last response was followed or closed before caller consumption")
					}
				} else if calls != 2 || redirect.closes != 1 || final.closes != 0 {
					t.Error("redirect intermediate/final body ownership changed")
				}
				if err != nil || string(data) != want {
					t.Fatalf("wrong response: data=%q err=%v", data, err)
				}
			})
		}
	}
}
