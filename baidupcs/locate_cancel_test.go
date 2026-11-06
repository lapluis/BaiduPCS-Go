package baidupcs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type locateBody struct {
	io.Reader
	closed bool
}

func (b *locateBody) Close() error { b.closed = true; return nil }

type locateBlockingReader struct {
	ctx              context.Context
	started, release chan struct{}
}

func (b locateBlockingReader) Read([]byte) (int, error) {
	close(b.started)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture forced release")
	}
}

func TestLocateDownloadCancellation(t *testing.T) {
	for _, uid := range []uint64{0, 1} {
		for _, phase := range []string{"before", "headers", "body"} {
			t.Run(phase+map[bool]string{true: "-uid", false: "-link"}[uid == 0], func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var body *locateBody
				calls := 0
				client := requester.NewHTTPClient()
				client.Client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Body != nil {
						defer r.Body.Close()
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
					body = &locateBody{Reader: locateBlockingReader{r.Context(), started, release}}
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				pcs := NewPCSWithClient(1, client)
				pcs.SetUID(uid)
				if phase == "before" {
					cancel()
				}
				done, joined := make(chan error, 1), make(chan struct{})
				go func() { defer close(joined); _, err := pcs.LocateDownloadWithContext(ctx, "/fixture"); done <- err }()
				defer func() { cancel(); unblock(); <-joined }()
				if phase != "before" {
					select {
					case <-started:
					case <-time.After(time.Second):
						t.Fatal("request did not reach cancellation stage")
					}
					cancel()
				}
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel error lost: %v", err)
					}
					if body != nil && !body.closed {
						t.Fatal("response body leaked")
					}
					if phase == "before" && calls != 0 || phase != "before" && calls != 1 {
						t.Fatalf("unexpected requests: %d", calls)
					}
					if uid == 0 && pcs.uid != 0 {
						t.Fatal("canceled UID cached")
					}
				case <-time.After(time.Second):
					t.Error("locate ignored cancellation")
				}
			})
		}
	}
}

func TestLocateDownloadUIDAndLegacyLookup(t *testing.T) {
	for _, reply := range []string{`{"error_code":"0","user":{"id":"42"}}`, `{"error_code":"1","error_msg":"fixture"}`, `{"error_code":"0","user":{"id":"0"}}`, `{"error_code":"0","user":{"id":"bad"}}`, `{`} {
		t.Run(reply, func(t *testing.T) {
			calls := 0
			var bodies []*locateBody
			client := requester.NewHTTPClient()
			client.Client.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Body != nil {
					defer r.Body.Close()
				}
				payload := reply
				if calls == 1 {
					if r.URL.Host != "tieba.baidu.com" || r.Method != http.MethodPost {
						t.Errorf("wrong UID endpoint: %s %s", r.Method, r.URL)
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Form.Get("bdusstoken") != "|null" || r.Form.Get("sign") == "" {
						t.Error("missing login fields/signature")
					}
				} else {
					if calls > 3 || r.URL.Query().Get("method") != "locatedownload" || r.URL.Query().Get("path") != "/fixture" {
						t.Errorf("unexpected follow-up request: %s", r.URL)
					}
					payload = `{"error_code":0,"urls":[{"url":"http://fixture.invalid/file","encrypt":0}]}`
				}
				body := &locateBody{Reader: strings.NewReader(payload)}
				bodies = append(bodies, body)
				resp := response(r, "")
				resp.Body = body
				return resp, nil
			})
			pcs := NewPCSWithClient(0, client)
			info, err := pcs.LocateDownload("/fixture")
			if strings.Contains(reply, `"42"`) {
				if err != nil || info == nil || len(info.URLs) != 1 || pcs.uid != 42 || calls != 2 {
					t.Fatalf("UID/link lookup failed: info=%v err=%v calls=%d uid=%d", info, err, calls, pcs.uid)
				}
				if _, err := pcs.LocateDownload("/fixture"); err != nil || calls != 3 {
					t.Fatalf("cached UID was not reused: calls=%d err=%v", calls, err)
				}
			} else if err == nil || info != nil || calls != 1 || pcs.uid != 0 {
				t.Fatalf("invalid UID accepted: info=%v err=%v calls=%d", info, err, calls)
			}
			for _, body := range bodies {
				if !body.closed {
					t.Error("response body leaked")
				}
			}
		})
	}
}

func TestLocateDownloadUIDRejectsHTTPFailure(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Body != nil {
			defer r.Body.Close()
		}
		resp := response(r, `{"error_code":"0","user":{"id":"42"}}`)
		resp.StatusCode = http.StatusServiceUnavailable
		return resp, nil
	})
	info, err := pcs.LocateDownloadWithContext(context.Background(), "/fixture")
	if err == nil || info != nil || pcs.uid != 0 || calls != 1 {
		t.Fatalf("HTTP failure accepted: info=%v err=%v calls=%d uid=%d", info, err, calls, pcs.uid)
	}
}
