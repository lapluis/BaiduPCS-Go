package rc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type mutationCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r mutationCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestServerMutationCancellation(t *testing.T) {
	for _, action := range []string{"copy", "move", "remove"} {
		for _, phase := range []string{"headers", "body", "reply", "normal"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				calls, mutations := 0, 0
				committed := false
				var bodies []*backendCancelBody
				client := requester.NewHTTPClient()
				client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
					}
					method := r.URL.Query().Get("method")
					payload := `{"error_code":0}`
					if method == "meta" {
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							return nil, err
						}
						defer r.MultipartForm.RemoveAll()
						var param baidupcs.PathsListJSON
						if err := json.Unmarshal([]byte(r.FormValue("param")), &param); err != nil || len(param.List) != 1 {
							return nil, fmt.Errorf("invalid metadata fixture: %v", err)
						}
						p := param.List[0].Path
						exists := p == "/source"
						id := 7
						if committed {
							if action == "move" || action == "remove" {
								exists = false
							}
							if p == "/target" && action != "remove" {
								exists = true
								if action == "copy" {
									id = 8
								}
							}
						}
						if exists {
							payload = fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":%d,"path":%q,"size":3,"mtime":0}]}`, id, p)
						} else {
							payload = `{"error_code":31066}`
						}
					} else {
						mutations++
						want := action
						if action == "remove" {
							want = "delete"
						}
						if method != want || mutations != 1 {
							return nil, fmt.Errorf("unexpected mutation %q", method)
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
						committed = phase == "normal"
					}
					body := &backendCancelBody{Reader: strings.NewReader(payload)}
					if method != "meta" {
						if phase == "body" {
							body.Reader = backendBlockingReader{r.Context(), started, release}
						} else if phase == "reply" {
							body.Reader = mutationCancelReader{body.Reader, cancel}
						}
					}
					bodies = append(bodies, body)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
				})
				remote := &Remote{PCS: baidupcs.NewPCSWithClient(1, client), UID: 7}
				src, dst := Endpoint{FS: remote, Root: "/source"}, Endpoint{FS: remote, Root: "/target"}
				a := Action{Source: &Entry{Size: 3, ModTime: time.Unix(0, 0), ID: "7"}}
				done, joined := make(chan error, 1), make(chan struct{})
				go func() {
					defer close(joined)
					var err error
					switch action {
					case "copy":
						err = copyAction(ctx, src, dst, &a, Options{})
					case "move":
						var used bool
						used, err = tryServerMove(ctx, src, dst, a, Options{})
						if !used && err == nil {
							err = errors.New("server move not used")
						}
					case "remove":
						err = remote.Remove(ctx, "/source", false)
					}
					done <- err
				}()
				defer func() { cancel(); unblock(); <-joined }()
				if phase == "headers" || phase == "body" {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("mutation did not start")
					}
					cancel()
				}
				select {
				case err := <-done:
					if phase == "normal" {
						if err != nil || !committed {
							t.Fatalf("normal mutation failed: %v", err)
						}
					} else {
						wantCalls := 3
						if action == "remove" {
							wantCalls = 2
						}
						if !errors.Is(err, context.Canceled) || calls != wantCalls {
							t.Fatalf("mutation cancellation lost: err=%v calls=%d want=%d", err, calls, wantCalls)
						}
					}
					if mutations != 1 {
						t.Fatalf("unexpected mutations: %d", mutations)
					}
					for _, body := range bodies {
						if !body.closed {
							t.Error("mutation response not closed")
						}
					}
				case <-time.After(time.Second):
					t.Error("server mutation ignored cancellation")
				}
			})
		}
	}
}
