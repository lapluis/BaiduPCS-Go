package cachemap

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
	"testing"
	"time"
)

func TestLoadOrStoreReplacesExpiredValue(t *testing.T) {
	cache := &cacheUnit{}
	old := expires.NewDataExpires("old", time.Hour)
	cache.Store("key", old)
	old.SetExpires(true)
	fresh := expires.NewDataExpires("fresh", time.Hour)
	actual, loaded := cache.LoadOrStore("key", fresh)
	if loaded || actual != fresh {
		t.Fatal("expired entry did not accept new value")
	}
	if actual, ok := cache.Load("key"); !ok || actual != fresh {
		t.Fatal("replacement not stored")
	}
	other := expires.NewDataExpires("other", time.Hour)
	if actual, loaded := cache.LoadOrStore("key", other); !loaded || actual != fresh {
		t.Fatal("live value replaced")
	}
}

type replacedWhileChecking struct {
	expires.DataExpires
	replace func()
}

func (e *replacedWhileChecking) IsExpires() bool { e.replace(); return true }

func TestExpiryCleanupPreservesReplacement(t *testing.T) {
	for _, rangeMode := range []bool{false, true} {
		cache := &cacheUnit{}
		fresh := expires.NewDataExpires("fresh", time.Hour)
		stale := &replacedWhileChecking{DataExpires: expires.NewDataExpires("old", time.Hour), replace: func() { cache.Store("key", fresh) }}
		cache.unit.Store("key", &cacheEntry{value: stale})
		if rangeMode {
			cache.Range(func(interface{}, expires.DataExpires) bool { return true })
		} else {
			cache.Load("key")
		}
		if actual, ok := cache.Load("key"); !ok || actual != fresh {
			t.Fatal("expiry cleanup deleted a newer value")
		}
	}
}
