package pcscommand

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

type transferCancelBody struct {
	io.Reader
	closed bool
}

func (b *transferCancelBody) Close() error { b.closed = true; return nil }

type transferBlockingReader struct {
	ctx              context.Context
	started, release chan struct{}
}

func (b transferBlockingReader) Read([]byte) (int, error) {
	close(b.started)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture forced release")
	}
}

type transferCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r transferCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestShareTransferCancellation(t *testing.T) {
	stages := []struct {
		name     string
		request  int
		fallback bool
	}{{"page1", 1, false}, {"verify", 2, false}, {"page2", 3, false}, {"list", 4, false}, {"collect", 5, false}, {"fallback", 6, true}, {"submit", 6, false}}
	for _, stage := range stages {
		for _, phase := range []string{"headers", "body", "reply"} {
			t.Run(stage.name+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				calls := 0
				var bodies []*transferCancelBody
				client := requester.NewHTTPClient()
				client.Client.Transport = shareFixtureTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					if calls == stage.request && phase == "headers" {
						close(started)
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-release:
							return nil, errors.New("fixture forced release")
						}
					}
					payload := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
					switch r.URL.Path {
					case "/share/verify":
						payload = `{"errno":0}`
					case "/share/list":
						payload = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a.txt"},{"fs_id":2,"server_filename":"b.txt"}]}`
					case "/rest/2.0/pcs/file":
						payload = `{"error_code":0}`
						if r.URL.Query().Get("method") == "meta" {
							payload = `{"error_code":0,"list":[{"fs_id":4,"path":"/saved/a.txt等文件","isdir":1}]}`
						} else if stage.fallback {
							payload = `{"error_code":31061}`
						}
					case "/share/transfer":
						payload = `{"errno":0,"info":[{"path":"/saved/a.txt等文件/a.txt"},{"path":"/saved/a.txt等文件/b.txt"}]}`
					}
					body := &transferCancelBody{Reader: strings.NewReader(payload)}
					if calls == stage.request {
						if phase == "body" {
							body.Reader = transferBlockingReader{r.Context(), started, release}
						} else if phase == "reply" {
							body.Reader = transferCancelReader{body.Reader, cancel}
						}
					}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Request: r, Body: body}, nil
				})
				pcs := baidupcs.NewPCSWithClient(1, client)
				done, joined := make(chan error, 1), make(chan struct{})
				go func() {
					defer close(joined)
					_, err := ShareTransferToWithContext(ctx, pcs, []string{"https://pan.baidu.com/s/1fixture", "abcd"}, "/saved", &baidupcs.TransferOption{Collect: true})
					done <- err
				}()
				defer func() { cancel(); unblock(); <-joined }()
				if phase != "reply" {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("transfer did not reach requested stage")
					}
					cancel()
				}
				select {
				case err := <-done:
					if ctx.Err() != context.Canceled || !errors.Is(err, context.Canceled) || calls != stage.request {
						t.Fatalf("cancellation lost: err=%v calls=%d want=%d", err, calls, stage.request)
					}
					for _, body := range bodies {
						if !body.closed {
							t.Error("transfer response not closed")
						}
					}
				case <-time.After(time.Second):
					t.Error("transfer ignored cancellation")
				}
			})
		}
	}
}
