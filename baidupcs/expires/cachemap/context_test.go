package cachemap

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

func TestCacheContextWaitDoesNotUnlockAnotherCaller(t *testing.T) {
	cm := &CacheOpMap{}
	cache := cm.LazyInitCachePoolOp("fixture")
	cache.LockKey("held")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		_, err := cm.CacheOperationWithContext(ctx, "fixture", "held", func() (expires.DataExpires, error) {
			calls.Add(1)
			return expires.NewDataExpires("wrong", time.Minute), nil
		})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("canceled waiter remained blocked")
	}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelProbe()
	_, probeErr := cm.CacheOperationWithContext(probeCtx, "fixture", "held", func() (expires.DataExpires, error) {
		t.Error("waiter released another caller's lock")
		return nil, nil
	})
	if !errors.Is(probeErr, context.DeadlineExceeded) {
		t.Errorf("held lock no longer exclusive: %v", probeErr)
	}
	cache.UnlockKey("held")
	data, err := cm.CacheOperationWithContext(context.Background(), "fixture", "held", func() (expires.DataExpires, error) {
		return expires.NewDataExpires("fresh", time.Minute), nil
	})
	if err != nil || data == nil || data.Data() != "fresh" || calls.Load() != 0 {
		t.Fatalf("subsequent operation: data=%v err=%v canceled calls=%d", data, err, calls.Load())
	}
}

func TestCacheContextCanceledResultNotPublished(t *testing.T) {
	cm := &CacheOpMap{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	data, err := cm.CacheOperationWithContext(ctx, "fixture", "key", func() (expires.DataExpires, error) {
		cancel()
		return expires.NewDataExpires("canceled", time.Minute), nil
	})
	if data != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, %v", data, err)
	}
	if _, ok := cm.LazyInitCachePoolOp("fixture").Load("key"); ok {
		t.Fatal("published canceled callback result")
	}
}

func TestCacheContextCoalescesConcurrentRequests(t *testing.T) {
	cm := &CacheOpMap{}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := cm.CacheOperationWithContext(context.Background(), "fixture", "shared", func() (expires.DataExpires, error) {
				calls.Add(1)
				return expires.NewDataExpires("shared result", time.Minute), nil
			})
			if err != nil || data == nil || data.Data() != "shared result" {
				t.Errorf("got %v, %v", data, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("performed %d callbacks", calls.Load())
	}
}
