package uploader

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type singleOwnedResponse struct {
	io.Reader
	reads, closes atomic.Int32
}

func (b *singleOwnedResponse) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.Reader.Read(p)
}

func (b *singleOwnedResponse) Close() error { b.closes.Add(1); return nil }

type singleResponseTransport func(*http.Request) (*http.Response, error)

func (tr singleResponseTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return tr(r)
}

func TestSingleUploadResponseOwnership(t *testing.T) {
	for _, mode := range []string{"no-checker", "checker-closes", "checker-retains"} {
		t.Run(mode, func(t *testing.T) {
			body := &singleOwnedResponse{Reader: strings.NewReader("result")}
			client := requester.NewHTTPClient()
			client.Client.Transport = singleResponseTransport(func(r *http.Request) (*http.Response, error) {
				defer r.Body.Close()
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, ContentLength: 6, Header: make(http.Header), Body: body, Request: r}, nil
			})
			u := NewUploader("https://fixture.invalid/upload", resumeReader{bytes.NewReader([]byte("payload"))})
			u.SetClient(client)
			var retained *http.Response
			if mode != "no-checker" {
				u.SetCheckFunc(func(resp *http.Response, err error) {
					if err != nil || resp == nil {
						t.Fatalf("upload: response=%v err=%v", resp, err)
					}
					if mode == "checker-retains" {
						retained = resp
						return
					}
					data, err := io.ReadAll(resp.Body)
					if err != nil || string(data) != "result" {
						t.Errorf("checker could not consume response: %q %v", data, err)
					}
					_ = resp.Body.Close()
				})
			}
			finished := make(chan int32, 1)
			u.OnFinish(func() { finished <- body.closes.Load() })
			u.Execute()
			wantCloses := int32(1)
			if mode == "checker-retains" {
				wantCloses = 0
			}
			select {
			case closes := <-finished:
				if closes != wantCloses {
					t.Errorf("finish observed closes=%d want=%d", closes, wantCloses)
				}
			case <-time.After(time.Second):
				t.Fatal("finish event did not run")
			}
			if body.closes.Load() != wantCloses || mode == "no-checker" && body.reads.Load() != 0 {
				t.Errorf("wrong response ownership: reads=%d closes=%d", body.reads.Load(), body.closes.Load())
			}
			if retained != nil {
				data, err := io.ReadAll(retained.Body)
				_ = retained.Body.Close()
				if err != nil || string(data) != "result" || body.closes.Load() != 1 {
					t.Errorf("retained response was not usable: %q %v", data, err)
				}
			}
		})
	}
}

func TestSingleUploadWithoutCheckerClosesOwnedConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "unused result")
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
	u.Execute()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("unused response retained the upload connection")
	}
}
