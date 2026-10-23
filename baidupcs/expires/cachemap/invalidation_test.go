package cachemap_test

import (
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires/cachemap"
)

func TestInvalidationRejectsPendingCachePublication(t *testing.T) {
	for _, withError := range []bool{false, true} {
		name := "CacheOperation"
		if withError {
			name += "WithError"
		}
		t.Run(name, func(t *testing.T) {
			cm := &cachemap.CacheOpMap{}
			cache := cm.LazyInitCachePoolOp("list")
			cache.Store("keep", expires.NewDataExpires("unchanged", time.Minute))
			cache.Store("remove", expires.NewDataExpires("stale", time.Minute))
			run := func(f func() expires.DataExpires) (expires.DataExpires, error) {
				if withError {
					return cm.CacheOperationWithError("list", "pending", func() (expires.DataExpires, error) { return f(), nil })
				}
				return cm.CacheOperation("list", "pending", f), nil
			}
			started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
			go func() {
				_, err := run(func() expires.DataExpires {
					close(started)
					<-release
					return expires.NewDataExpires("old", time.Minute)
				})
				done <- err
			}()
			<-started
			cm.InvalidateKeys("list", func(key interface{}) bool { return key != "keep" })
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if _, ok := cache.Load("remove"); ok {
				t.Fatal("matching entry survived invalidation")
			}
			if data, ok := cache.Load("keep"); !ok || data.Data() != "unchanged" {
				t.Fatal("unrelated entry was invalidated")
			}
			if _, ok := cache.Load("pending"); ok {
				t.Fatal("pending response restored stale cache")
			}
			data, err := run(func() expires.DataExpires { return expires.NewDataExpires("new", time.Minute) })
			if err != nil || data == nil || data.Data() != "new" {
				t.Fatalf("fresh fetch: %v, %v", data, err)
			}
			if _, ok := cache.Load("pending"); !ok {
				t.Fatal("fresh response was not cached")
			}
		})
	}
}
