package requester

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRequestConstructionFailureClosesBody(t *testing.T) {
	for _, fixture := range []struct{ name, method, url string }{
		{"invalid-method", "POST\n", "https://fixture.invalid/upload"},
		{"invalid-url", http.MethodPost, "http://[invalid"},
		{"invalid-escape", http.MethodPost, "https://fixture.invalid/%zz"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			body := &cancelPreparationBody{}
			transport := &clientFixtureTransport{}
			client := NewHTTPClient()
			client.Client.Transport = transport
			resp, err := client.ReqWithContext(context.Background(), fixture.method, fixture.url, body, nil)
			if err == nil || resp != nil || transport.calls != 0 {
				t.Fatalf("invalid request reached transport: response=%v err=%v calls=%d", resp, err, transport.calls)
			}
			if body.closeCalls != 1 {
				t.Fatalf("request body closed %d times, want 1", body.closeCalls)
			}
		})
	}
}

type bodyOwnershipTransport struct {
	body io.ReadCloser
	err  error
}

func (tr *bodyOwnershipTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.body = req.Body
	if tr.err != nil {
		req.Body.Close()
		return nil, tr.err
	}
	// A RoundTripper may close the request body asynchronously, after returning.
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
}

func TestRequestTransfersBodyOwnershipToTransport(t *testing.T) {
	failure := errors.New("synthetic transport failure")
	for _, transportErr := range []error{nil, failure} {
		name := "success"
		if transportErr != nil {
			name = "transport-error"
		}
		t.Run(name, func(t *testing.T) {
			body := &cancelPreparationBody{}
			transport := &bodyOwnershipTransport{err: transportErr}
			client := NewHTTPClient()
			client.Client.Transport = transport
			resp, err := client.ReqWithContext(context.Background(), http.MethodPost, "https://fixture.invalid/upload", body, nil)
			if resp != nil {
				resp.Body.Close()
			}
			if transport.body != body || !errors.Is(err, transportErr) {
				t.Fatalf("body or error lost: body=%v err=%v", transport.body, err)
			}
			if transportErr == nil {
				if body.closeCalls != 0 {
					t.Fatalf("request helper closed transport-owned body early: %d", body.closeCalls)
				}
				transport.body.Close()
			}
			if body.closeCalls != 1 {
				t.Fatalf("request body closed %d times, want 1", body.closeCalls)
			}
		})
	}
}
