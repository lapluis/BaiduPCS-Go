package baidupcs

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestPreparationCacheWaitCanBeCanceled(t *testing.T) {
	for _, kind := range []string{"list", "uk"} {
		t.Run(kind, func(t *testing.T) {
			started, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				data := `{"errno":0,"list":[]}`
				if kind == "uk" {
					data = `{"errno":0,"records":[{"uk":7}]}`
				}
				return response(r, data), nil
			})
			fetch := func(ctx context.Context) pcserror.Error {
				if kind == "uk" {
					_, err := pcs.CacheUKWithContext(ctx)
					return err
				}
				_, err := pcs.CacheFilesDirectoriesListWithContext(ctx, "/", nil)
				return err
			}
			var ownerErr error
			go func() { ownerErr = fetch(context.Background()); close(ownerDone) }()
			defer func() { unblock(); <-ownerDone }()
			<-started
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan pcserror.Error, 1)
			go func() { done <- fetch(ctx) }()
			select {
			case err := <-done:
				t.Fatalf("cache waiter returned before the owner: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if err == nil || !errors.Is(err.GetError(), context.Canceled) {
					t.Fatalf("cache wait lost cancellation cause: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cache waiter ignored cancellation")
			}
			select {
			case <-ownerDone:
				t.Fatal("canceling the waiter stopped the cache owner")
			default:
			}
			unblock()
			<-ownerDone
			if ownerErr != nil {
				t.Fatal(ownerErr)
			}
			if err := fetch(context.Background()); err != nil || calls.Load() != 1 {
				t.Fatalf("successful owner response was not cached: calls=%d err=%v", calls.Load(), err)
			}
			if err := fetch(ctx); err == nil || !errors.Is(err.GetError(), context.Canceled) || calls.Load() != 1 {
				t.Fatalf("warm cache ignored canceled context: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}
