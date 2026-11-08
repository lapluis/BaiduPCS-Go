package cachemap

import (
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

type replacedAfterLiveCheck struct {
	expires.DataExpires
	replace func()
	checks  int
}

func (e *replacedAfterLiveCheck) IsExpires() bool {
	e.checks++
	if e.checks == 1 {
		// Model a replacement just after the old entry was observed live;
		// the old deadline has elapsed by the time it is checked again.
		e.replace()
		return false
	}
	return true
}

func TestClearInvalidatePreservesNewerReplacement(t *testing.T) {
	cacheMap := &CacheOpMap{}
	cache := cacheMap.LazyInitCachePoolOp("fixture").(*cacheUnit)
	fresh := expires.NewDataExpires("fresh", time.Hour)
	old := &replacedAfterLiveCheck{
		DataExpires: expires.NewDataExpires("old", time.Hour),
		replace:     func() { cache.Store("key", fresh) },
	}
	// Bypass Store's initial expiry check so the controlled interleaving
	// occurs specifically while ClearInvalidate traverses the old entry.
	cache.unit.Store("key", &cacheEntry{value: old})
	cacheMap.ClearInvalidate()
	if actual, ok := cache.Load("key"); !ok || actual != fresh {
		t.Fatal("cleanup of an old value removed a newer replacement")
	}
	fresh.SetExpires(true)
	cacheMap.ClearInvalidate()
	if _, ok := cache.Load("key"); ok {
		t.Fatal("cleanup retained a genuinely expired entry")
	}
}
