package uploader

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type policyUploadTransport struct{ idleCloses int }

func (tr *policyUploadTransport) CloseIdleConnections() { tr.idleCloses++ }

func (*policyUploadTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	_, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
}

func TestSingleUploadKeepsCallerClientPolicy(t *testing.T) {
	for _, mode := range []string{"standard", "custom", "nil-active", "zero-client"} {
		t.Run(mode, func(t *testing.T) {
			client := requester.NewHTTPClient()
			client.SetTimeout(17 * time.Second)
			client.SetResponseHeaderTimeout(7 * time.Second)
			originalTransport := client.Client.Transport.(*http.Transport)
			custom := &policyUploadTransport{}
			switch mode {
			case "custom":
				client.Client.Transport = custom
			case "nil-active":
				client.Client.Transport = nil
			case "zero-client":
				client = &requester.HTTPClient{}
				client.SetTimeout(17 * time.Second)
			}
			active := client.Client.Transport
			u := NewUploader("https://fixture.invalid/upload", resumeReader{bytes.NewReader([]byte("payload"))})
			u.SetClient(client)
			u.lazyInit()
			if client.Client.Timeout != 17*time.Second || client.Client.Transport != active || originalTransport.ResponseHeaderTimeout != 7*time.Second {
				t.Fatalf("upload changed shared client: timeout=%v transport=%T header timeout=%v", client.Client.Timeout, client.Client.Transport, originalTransport.ResponseHeaderTimeout)
			}
			if u.client == client || u.client.Client.Timeout != 0 || u.client.Client.Jar != client.Client.Jar {
				t.Fatal("upload policy was not isolated while retaining jar")
			}
			if mode == "custom" {
				if u.client.Client.Transport != custom {
					t.Fatal("custom transport replaced")
				}
				resp, _, err := u.execute()
				if err != nil || resp == nil {
					t.Fatal("custom transport upload failed", err)
				}
				resp.Body.Close()
				if custom.idleCloses != 0 {
					t.Fatal("upload closed shared custom transport")
				}
			} else {
				transport, ok := u.client.Client.Transport.(*http.Transport)
				if !ok || transport == originalTransport || transport.ResponseHeaderTimeout != 10*time.Second {
					t.Fatal("upload transport policy incorrect", transport)
				}
			}
		})
	}
}

func TestSingleUploadClosesOwnedTransportConnections(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	u := NewUploader(server.URL, resumeReader{bytes.NewReader([]byte("payload"))})
	u.SetClient(requester.NewHTTPClient())
	u.SetCheckFunc(func(resp *http.Response, err error) {
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		if data, err := io.ReadAll(resp.Body); err != nil || string(data) != "ok" {
			t.Error("upload response unreadable", string(data), err)
		}
	})
	u.Execute()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("owned upload transport kept its connection alive")
	}
}
