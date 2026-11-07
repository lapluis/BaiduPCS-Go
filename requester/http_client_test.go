package requester

import (
	"crypto/tls"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type clientFixtureTransport struct{ calls int }

func (t *clientFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("fixture")), Request: r}, nil
}

func TestHTTPClientPreservesCustomRoundTripper(t *testing.T) {
	for name, configure := range map[string]func(*HTTPClient){
		"first_request":  func(*HTTPClient) {},
		"keepalive":      func(c *HTTPClient) { c.SetKeepAlive(false) },
		"compression":    func(c *HTTPClient) { c.SetGzip(false) },
		"header_timeout": func(c *HTTPClient) { c.SetResponseHeaderTimeout(time.Second) },
		"TLS_timeout":    func(c *HTTPClient) { c.SetTLSHandshakeTimeout(time.Second) },
		"TLS_setting":    func(c *HTTPClient) { c.SetHTTPSecure(true) },
		"proxy":          func(c *HTTPClient) { c.SetProxy("http://127.0.0.1:1") },
	} {
		for _, zeroValue := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/constructed", true: "/zero_value"}[zeroValue], func(t *testing.T) {
				client := NewHTTPClient()
				if zeroValue {
					client = &HTTPClient{}
				}
				transport := &clientFixtureTransport{}
				client.Client.Transport = transport
				configure(client)
				// The fixture scheme cannot reach the network if initialization
				// mistakenly replaces our transport with the default transport.
				resp, err := client.Req(http.MethodGet, "fixture://local/", nil, nil)
				if err != nil {
					t.Fatalf("custom transport bypassed: %v", err)
				}
				defer resp.Body.Close()
				if client.Client.Transport != transport || transport.calls != 1 {
					t.Fatal("custom transport was replaced")
				}
			})
		}
	}
}

func TestHTTPClientConfiguresProvidedHTTPTransport(t *testing.T) {
	for _, initializeFirst := range []bool{false, true} {
		client := NewHTTPClient()
		if initializeFirst {
			client.SetKeepAlive(true)
		}
		tlsConfig := &tls.Config{ServerName: "fixture.invalid"}
		transport := &http.Transport{TLSClientConfig: tlsConfig}
		client.Client.Transport = transport
		client.SetKeepAlive(false)
		client.SetGzip(false)
		client.SetResponseHeaderTimeout(2 * time.Second)
		client.SetTLSHandshakeTimeout(3 * time.Second)
		if client.Client.Transport != transport || !transport.DisableKeepAlives || !transport.DisableCompression || transport.ResponseHeaderTimeout != 2*time.Second || transport.TLSHandshakeTimeout != 3*time.Second {
			t.Fatal("settings did not apply to the provided HTTP transport")
		}
		if transport.TLSClientConfig != tlsConfig {
			t.Fatal("unrelated custom TLS settings were discarded")
		}
	}
}

func TestHTTPClientCloneIsolatesStandardTransportPolicy(t *testing.T) {
	for _, separateTransport := range []bool{false, true} {
		client := NewHTTPClient()
		client.SetResponseHeaderTimeout(7 * time.Second)
		if separateTransport {
			client.Client.Transport = &http.Transport{ResponseHeaderTimeout: 7 * time.Second}
		}
		original := client.Client.Transport.(*http.Transport)
		tlsConfig := &tls.Config{ServerName: "fixture.invalid"}
		original.TLSClientConfig = tlsConfig
		clone := client.Clone()
		clone.SetResponseHeaderTimeout(3 * time.Second)
		clone.SetHTTPSecure(false)
		transport := clone.Client.Transport.(*http.Transport)
		if transport == original || clone.transport == client.transport || transport.ResponseHeaderTimeout != 3*time.Second || original.ResponseHeaderTimeout != 7*time.Second {
			t.Fatal("cloned transport policy aliases caller transport")
		}
		if original.TLSClientConfig != tlsConfig || tlsConfig.InsecureSkipVerify || transport.TLSClientConfig == tlsConfig || !transport.TLSClientConfig.InsecureSkipVerify || transport.TLSClientConfig.ServerName != tlsConfig.ServerName {
			t.Fatal("TLS configuration lost or modified on original transport")
		}
		clone.Client.Transport = nil
		clone.SetResponseHeaderTimeout(9 * time.Second)
		if client.transport.ResponseHeaderTimeout != 7*time.Second || clone.transport.ResponseHeaderTimeout != 9*time.Second {
			t.Fatal("fallback transport policy remains shared")
		}
	}
}
