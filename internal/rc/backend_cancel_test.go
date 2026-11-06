package rc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type backendCancelBody struct {
	io.Reader
	closed bool
}

func (b *backendCancelBody) Close() error { b.closed = true; return nil }

type backendBlockingReader struct {
	ctx              context.Context
	started, release chan struct{}
}

func (b backendBlockingReader) Read([]byte) (int, error) {
	close(b.started)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture forced release")
	}
}

func TestRemoteMetadataAndMkdirCancellation(t *testing.T) {
	for _, action := range []string{"stat", "list", "mkdir"} {
		for _, phase := range []string{"headers", "body"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				calls := 0
				var bodies []*backendCancelBody
				client := requester.NewHTTPClient()
				client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					if action == "mkdir" && calls == 1 {
						body := &backendCancelBody{Reader: strings.NewReader(`{"error_code":31066}`)}
						bodies = append(bodies, body)
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
					}
					if phase == "headers" {
						close(started)
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-release:
							return nil, errors.New("fixture forced release")
						}
					}
					body := &backendCancelBody{Reader: backendBlockingReader{r.Context(), started, release}}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
				})
				remote := &Remote{PCS: baidupcs.NewPCSWithClient(1, client), UID: 7}
				done, joined := make(chan error, 1), make(chan struct{})
				go func() {
					defer close(joined)
					var err error
					switch action {
					case "stat":
						_, err = remote.Stat(ctx, "/new")
					case "list":
						_, err = remote.List(ctx, "/new")
					default:
						err = remote.Mkdir(ctx, "/new")
					}
					done <- err
				}()
				defer func() { cancel(); unblock(); <-joined }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("backend request did not start")
				}
				cancel()
				select {
				case err := <-done:
					want := 1
					if action == "mkdir" {
						want = 2
					}
					if !errors.Is(err, context.Canceled) || calls != want {
						t.Fatalf("backend cancellation lost: err=%v calls=%d", err, calls)
					}
					for _, body := range bodies {
						if !body.closed {
							t.Error("backend response not closed")
						}
					}
				case <-time.After(time.Second):
					t.Error("backend ignored cancellation")
				}
			})
		}
	}
}
