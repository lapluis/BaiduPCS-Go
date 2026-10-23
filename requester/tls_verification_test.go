package requester

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientTLSVerification(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "local TLS fixture")
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	defer server.Close()
	for _, tc := range []struct {
		name        string
		configure   func(*HTTPClient)
		wantSuccess bool
	}{
		{"default", func(*HTTPClient) {}, false},
		{"enabled", func(c *HTTPClient) { c.SetHTTPSecure(true) }, false},
		{"explicitly_disabled", func(c *HTTPClient) { c.SetHTTPSecure(false) }, true},
		{"reenabled", func(c *HTTPClient) { c.SetHTTPSecure(false); c.SetHTTPSecure(true) }, false},
	} {
		for _, zeroValue := range []bool{false, true} {
			name := tc.name
			if zeroValue {
				name += "/zero_value"
			}
			t.Run(name, func(t *testing.T) {
				client := NewHTTPClient()
				if zeroValue {
					client = &HTTPClient{}
				}
				client.configuredTransport().Proxy = nil
				tc.configure(client)
				defer client.CloseIdleConnections()
				resp, err := client.Req(http.MethodGet, server.URL, nil, nil)
				if resp != nil {
					resp.Body.Close()
				}
				if tc.wantSuccess && err != nil {
					t.Fatalf("explicit TLS opt-out failed: %v", err)
				}
				if !tc.wantSuccess && err == nil {
					t.Fatal("accepted an untrusted certificate")
				}
			})
		}
	}
}

func TestHTTPSecurePreservesCallerTLSConfig(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	original := &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}
	client := NewHTTPClient()
	transport := &http.Transport{TLSClientConfig: original}
	client.Transport = transport
	client.SetHTTPSecure(true)
	defer client.CloseIdleConnections()
	if transport.TLSClientConfig == original || !original.InsecureSkipVerify {
		t.Fatal("security setting mutated the caller's shared TLS config")
	}
	if transport.TLSClientConfig.RootCAs != roots || transport.TLSClientConfig.ServerName != original.ServerName || transport.TLSClientConfig.MinVersion != original.MinVersion || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("security setting discarded caller TLS options or disabled verification")
	}
	resp, err := client.Req(http.MethodGet, server.URL, nil, nil)
	if err != nil {
		t.Fatalf("trusted local certificate rejected: %v", err)
	}
	resp.Body.Close()
}
