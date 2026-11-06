package rc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type emptyUploadWaitBody struct {
	ctx              context.Context
	started, release chan struct{}
	closed           bool
}

type emptyUploadResultBody struct {
	*strings.Reader
	closed bool
}

func (b *emptyUploadResultBody) Close() error { b.closed = true; return nil }

func TestEmptyUploadSendsValidMultipartAndClosesResult(t *testing.T) {
	for _, remoteFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "remote-error"}[remoteFailure], func(t *testing.T) {
			response := `{"error_code":0}`
			if remoteFailure {
				response = `{"error_code":31066}`
			}
			body := &emptyUploadResultBody{Reader: strings.NewReader(response)}
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				defer r.Body.Close()
				if r.Method != http.MethodPost || r.URL.Query().Get("path") != "/fixture" {
					t.Errorf("wrong upload request: method=%s path=%s", r.Method, r.URL.Query().Get("path"))
				}
				mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || mediaType != "multipart/form-data" {
					t.Fatalf("invalid content type: %v %s", err, mediaType)
				}
				data, err := io.ReadAll(r.Body)
				if err != nil || int64(len(data)) != r.ContentLength {
					t.Fatalf("body length mismatch: bytes=%d declared=%d err=%v", len(data), r.ContentLength, err)
				}
				reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
				part, err := reader.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				payload, err := io.ReadAll(part)
				if err != nil || len(payload) != 0 || part.FormName() != "file" || part.FileName() != "empty" {
					t.Fatalf("invalid empty part: bytes=%d err=%v", len(payload), err)
				}
				if _, err := reader.NextPart(); err != io.EOF {
					t.Fatalf("unexpected extra part: %v", err)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
			})
			err := uploadEmptyFile(context.Background(), &baidupcs.BaiduPCS{}, "/fixture", client)
			if (err != nil) != remoteFailure || !body.closed {
				t.Fatalf("empty upload outcome: err=%v closed=%v", err, body.closed)
			}
		})
	}
}

func (b *emptyUploadWaitBody) Read([]byte) (int, error) {
	close(b.started)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture forced release")
	}
}
func (b *emptyUploadWaitBody) Close() error { b.closed = true; return nil }

func TestEmptyUploadCancellationCoversHeadersAndBody(t *testing.T) {
	for _, mode := range []string{"before-send", "headers", "body"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			releaseWaiter := sync.OnceFunc(func() { close(release) })
			defer releaseWaiter()
			calls := 0
			var body *emptyUploadWaitBody
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Body != nil {
					defer r.Body.Close()
				}
				if mode == "headers" {
					close(started)
					select {
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-release:
						return nil, errors.New("fixture forced release")
					}
				}
				if mode == "body" {
					body = &emptyUploadWaitBody{r.Context(), started, release, false}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error_code":0}`)), Request: r}, nil
			})
			if mode == "before-send" {
				cancel()
				err := uploadEmptyFile(ctx, &baidupcs.BaiduPCS{}, "/fixture", client)
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatalf("canceled empty upload sent: calls=%d err=%v", calls, err)
				}
				return
			}
			done := make(chan error, 1)
			joined := make(chan struct{})
			go func() { defer close(joined); done <- uploadEmptyFile(ctx, &baidupcs.BaiduPCS{}, "/fixture", client) }()
			defer func() { cancel(); releaseWaiter(); <-joined }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("empty upload did not reach wait stage")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
				if mode == "body" && !body.closed {
					t.Error("canceled response body not closed")
				}
			case <-time.After(time.Second):
				t.Error("empty upload ignored task cancellation")
			}
		})
	}
}
