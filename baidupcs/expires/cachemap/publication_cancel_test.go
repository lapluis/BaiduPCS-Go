package cachemap

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

type expiryCheckHook struct {
	expires.DataExpires
	check func()
}

func (v *expiryCheckHook) IsExpires() bool {
	v.check()
	return v.DataExpires.IsExpires()
}

func TestCacheCancellationAfterHitDoesNotReturnData(t *testing.T) {
	cm := &CacheOpMap{}
	cache := cm.LazyInitCachePoolOp("fixture").(*cacheUnit)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	value := &expiryCheckHook{DataExpires: expires.NewDataExpires("cached", time.Hour), check: cancel}
	cache.unit.Store("key", &cacheEntry{value: value})
	calls := 0
	data, err := cm.CacheOperationWithContext(ctx, "fixture", "key", func() (expires.DataExpires, error) {
		calls++
		return expires.NewDataExpires("unexpected", time.Hour), nil
	})
	if !errors.Is(err, context.Canceled) || data != nil || calls != 0 {
		t.Fatalf("canceled cache hit returned data: data=%v err=%v calls=%d", data != nil, err, calls)
	}
	value.check = func() {}
	data, err = cm.CacheOperationWithContext(context.Background(), "fixture", "key", func() (expires.DataExpires, error) {
		calls++
		return nil, errors.New("unexpected cache fill")
	})
	if err != nil || data == nil || data.Data() != "cached" || calls != 0 {
		t.Fatalf("canceled reader removed shared cached data: data=%v err=%v calls=%d", data != nil, err, calls)
	}
}

func TestCacheCancellationAfterExpiredLookupDoesNotRefill(t *testing.T) {
	cm := &CacheOpMap{}
	cache := cm.LazyInitCachePoolOp("fixture").(*cacheUnit)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	value := &expiryCheckHook{DataExpires: expires.NewDataExpires("expired", time.Hour), check: cancel}
	value.SetExpires(true)
	cache.unit.Store("key", &cacheEntry{value: value})
	calls := 0
	data, err := cm.CacheOperationWithContext(ctx, "fixture", "key", func() (expires.DataExpires, error) {
		calls++
		return expires.NewDataExpires("unexpected refill", time.Hour), nil
	})
	if !errors.Is(err, context.Canceled) || data != nil || calls != 0 {
		t.Fatalf("canceled expired lookup refilled cache: data=%v err=%v calls=%d", data != nil, err, calls)
	}
	if _, ok := cache.unit.Load("key"); ok {
		t.Fatal("expired lookup retained data")
	}
}

func TestCacheCancellationDuringPublicationDoesNotPublish(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := map[bool]string{false: "expiry-validation", true: "publication-lock"}[blocked]
		t.Run(name, func(t *testing.T) {
			cm := &CacheOpMap{}
			cache := cm.LazyInitCachePoolOp("fixture").(*cacheUnit)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checked := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { cache.publishMu.Unlock() }) }
			result := make(chan error, 1)
			go func() {
				data, err := cm.CacheOperationWithContext(ctx, "fixture", "key", func() (expires.DataExpires, error) {
					if blocked {
						cache.publishMu.Lock()
					}
					value := &expiryCheckHook{DataExpires: expires.NewDataExpires("canceled fill", time.Hour), check: func() {
						if !blocked {
							cancel()
						}
						close(checked)
					}}
					return value, nil
				})
				if err == nil || data != nil {
					t.Errorf("canceled publication returned data: data=%v err=%v", data != nil, err)
				}
				result <- err
			}()
			select {
			case <-checked:
			case <-time.After(time.Second):
				t.Fatal("publication did not reach expiry validation")
			}
			if blocked {
				defer release()
				cancel()
				release()
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("publication lost cancellation: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("publication did not return after release")
			}
			if _, ok := cache.unit.Load("key"); ok {
				t.Fatal("canceled fill was published")
			}
			fresh, err := cm.CacheOperationWithContext(context.Background(), "fixture", "key", func() (expires.DataExpires, error) {
				return expires.NewDataExpires("fresh", time.Hour), nil
			})
			if err != nil || fresh == nil || fresh.Data() != "fresh" {
				t.Fatalf("subsequent caller could not fill cache: data=%v err=%v", fresh != nil, err)
			}
		})
	}
}
