package pcsupload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type lifecycleResponseBody struct {
	ctx                 context.Context
	release             <-chan struct{}
	started, closed     chan struct{}
	readOnce, closeOnce sync.Once
}

func (b *lifecycleResponseBody) Read([]byte) (int, error) {
	b.readOnce.Do(func() { close(b.started) })
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture released")
	}
}
func (b *lifecycleResponseBody) Close() error { b.closeOnce.Do(func() { close(b.closed) }); return nil }

func TestPCSUploadLifecycleCancellationReachesHTTP(t *testing.T) {
	for _, stage := range []string{"precreate", "merge"} {
		for _, bodyBlocked := range []bool{false, true} {
			name := stage + "/headers"
			if bodyBlocked {
				name = stage + "/body"
			}
			t.Run(name, func(t *testing.T) {
				started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
					if bodyBlocked {
						return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: &lifecycleResponseBody{ctx: r.Context(), release: release, started: started, closed: closed}}, nil
					}
					close(started)
					select {
					case <-r.Context().Done():
						return nil, r.Context().Err()
					case <-release:
						return nil, errors.New("fixture released")
					}
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := make(chan error, 1)
				go func() {
					if stage == "precreate" {
						_, err := pu.Precreate(ctx)
						done <- err
					} else {
						done <- pu.CreateSuperFile(ctx, "fixture.invalid", "overwrite", "fixture", 4, map[int]string{0: "fixture"})
					}
				}()
				defer close(release)
				select {
				case <-started:
				case err := <-done:
					t.Fatalf("request returned early: %v", err)
				case <-time.After(time.Second):
					t.Fatal("request did not start")
				}
				cancel()
				select {
				case err := <-done:
					var pcsErr pcserror.Error
					if !errors.As(err, &pcsErr) || !errors.Is(pcsErr.GetError(), context.Canceled) {
						t.Fatalf("cancellation cause lost: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("HTTP stage ignored cancellation")
				}
				if bodyBlocked {
					select {
					case <-closed:
					default:
						t.Fatal("canceled response body was not closed")
					}
				}
			})
		}
	}
}

func TestPCSUploadLifecycleHealthyAndPreCanceled(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		calls := 0
		pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
			calls++
			payload := `{"error_code":0,"server":["fixture.invalid"]}`
			if r.Method == http.MethodPost {
				payload = `{"errno":0}`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		_, firstErr := pu.Precreate(ctx)
		secondErr := pu.CreateSuperFile(ctx, "fixture.invalid", "overwrite", "fixture", 4, map[int]string{0: "fixture"})
		cancel()
		if canceled {
			if firstErr == nil || secondErr == nil || calls != 0 {
				t.Fatalf("pre-canceled operation reached HTTP: %v, %v calls=%d", firstErr, secondErr, calls)
			}
		} else if firstErr != nil || secondErr != nil || calls != 2 {
			t.Fatalf("healthy operation failed: %v, %v calls=%d", firstErr, secondErr, calls)
		}
	}
}
