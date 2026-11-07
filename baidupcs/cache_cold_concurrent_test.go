package baidupcs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coldCacheResponseBody struct {
	*strings.Reader
	closed *atomic.Int32
}

func (b *coldCacheResponseBody) Close() error { b.closed.Add(1); return nil }

func TestColdConcurrentPreparationCacheSharesOneRequest(t *testing.T) {
	for _, kind := range []string{"uk", "list"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var first sync.Once
			var calls, closes atomic.Int32
			var joined sync.WaitGroup
			t.Cleanup(func() { cancel(); unblock(); joined.Wait() })
			pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
				calls.Add(1)
				first.Do(func() { close(started) })
				select {
				case <-release:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
				payload := `{"errno":0,"records":[{"uk":7}]}`
				if kind == "list" {
					payload = `{"errno":0,"list":[{"fs_id":7,"path":"/fixture/file","server_filename":"file","size":3}]}`
				}
				resp := response(req, "")
				resp.Body = &coldCacheResponseBody{Reader: strings.NewReader(payload), closed: &closes}
				return resp, nil
			})
			fetch := func(ctx context.Context) (string, error) {
				if kind == "uk" {
					value, err := pcs.CacheUKWithContext(ctx)
					return fmt.Sprint(value), err
				}
				list, err := pcs.CacheFilesDirectoriesListWithContext(ctx, "/fixture", nil)
				if err != nil {
					return "", err
				}
				if len(list) != 1 || list[0] == nil {
					return "", fmt.Errorf("unexpected cached listing size %d", len(list))
				}
				return list[0].Path, nil
			}
			type result struct {
				value string
				err   error
			}
			start := make(chan struct{})
			ready, results := make(chan struct{}, 32), make(chan result, 32)
			for worker := 0; worker < 32; worker++ {
				joined.Add(1)
				go func() {
					defer joined.Done()
					<-start
					ready <- struct{}{}
					value, err := fetch(ctx)
					results <- result{value, err}
				}()
			}
			close(start)
			for worker := 0; worker < 32; worker++ {
				<-ready
			}
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("cold cache did not start its owner request")
			}
			waitCtx, stopWaiter := context.WithCancel(ctx)
			defer stopWaiter()
			waiter := make(chan error, 1)
			joined.Add(1)
			go func() { defer joined.Done(); _, err := fetch(waitCtx); waiter <- err }()
			select {
			case err := <-waiter:
				t.Fatalf("cache waiter returned while owner was blocked: %v", err)
			case <-time.After(30 * time.Millisecond):
			}
			stopWaiter()
			select {
			case err := <-waiter:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cache waiter lost cancellation: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("canceled cache waiter did not finish")
			}
			if calls.Load() != 1 || closes.Load() != 0 {
				t.Fatalf("cold requests were not merged or waiter stopped owner: calls=%d closes=%d", calls.Load(), closes.Load())
			}
			unblock()
			want := "7"
			if kind == "list" {
				want = "/fixture/file"
			}
			for worker := 0; worker < 32; worker++ {
				select {
				case got := <-results:
					if got.err != nil || got.value != want {
						t.Errorf("cold cohort lost result: value=%q err=%v", got.value, got.err)
					}
				case <-ctx.Done():
					t.Fatal("cold cache cohort did not finish")
				}
			}
			if value, err := fetch(ctx); err != nil || value != want || calls.Load() != 1 || closes.Load() != 1 {
				t.Fatalf("warm cache or response ownership changed: value=%q err=%v calls=%d closes=%d", value, err, calls.Load(), closes.Load())
			}
		})
	}
}
