package cachemap

import (
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
	"sync"
)

type (
	CacheUnit interface {
		Delete(key interface{})
		Load(key interface{}) (value expires.DataExpires, ok bool)
		LoadOrStore(key interface{}, value expires.DataExpires) (actual expires.DataExpires, loaded bool)
		Range(f func(key interface{}, value expires.DataExpires) bool)
		Store(key interface{}, value expires.DataExpires)
		LockKey(key interface{})
		UnlockKey(key interface{})
	}

	cacheUnit struct {
		unit      sync.Map
		keyMap    sync.Map
		publishMu sync.Mutex
		revision  uint64
	}
	cacheEntry struct{ value expires.DataExpires }
)

func (cu *cacheUnit) currentRevision() uint64 {
	cu.publishMu.Lock()
	defer cu.publishMu.Unlock()
	return cu.revision
}

func (cu *cacheUnit) storeAtRevision(key interface{}, value expires.DataExpires, revision uint64) {
	if value == nil || value.IsExpires() {
		return
	}
	cu.publishMu.Lock()
	defer cu.publishMu.Unlock()
	if revision == cu.revision {
		cu.unit.Store(key, &cacheEntry{value: value})
	}
}

func (cu *cacheUnit) invalidateKeys(match func(interface{}) bool) {
	cu.publishMu.Lock()
	defer cu.publishMu.Unlock()
	cu.revision++
	cu.unit.Range(func(key, _ interface{}) bool {
		if match(key) {
			cu.unit.Delete(key)
		}
		return true
	})
}

func (cu *cacheUnit) Delete(key interface{}) {
	cu.unit.Delete(key)
	// A key lock may still be held or have waiters. Replacing it would split
	// callers across different mutexes and strand the original lock.
}

func (cu *cacheUnit) Load(key interface{}) (value expires.DataExpires, ok bool) {
	val, ok := cu.unit.Load(key)
	if !ok {
		return nil, ok
	}
	entry := val.(*cacheEntry)
	exp := entry.value
	if exp.IsExpires() {
		cu.unit.CompareAndDelete(key, entry)
		return nil, false
	}
	return exp, ok
}

func (cu *cacheUnit) Range(f func(key interface{}, value expires.DataExpires) bool) {
	cu.unit.Range(func(k, val interface{}) bool {
		entry := val.(*cacheEntry)
		exp := entry.value
		if exp.IsExpires() {
			cu.unit.CompareAndDelete(k, entry)
			return true
		}
		return f(k, exp)
	})
}

func (cu *cacheUnit) LoadOrStore(key interface{}, value expires.DataExpires) (actual expires.DataExpires, loaded bool) {
	if value == nil || value.IsExpires() {
		return cu.Load(key)
	}
	for {
		ac, loaded := cu.unit.LoadOrStore(key, &cacheEntry{value: value})
		entry := ac.(*cacheEntry)
		if !entry.value.IsExpires() {
			return entry.value, loaded
		}
		cu.unit.CompareAndDelete(key, entry)
		if value.IsExpires() {
			return nil, false
		}
	}
}

func (cu *cacheUnit) Store(key interface{}, value expires.DataExpires) {
	if value == nil || value.IsExpires() {
		return
	}
	cu.unit.Store(key, &cacheEntry{value: value})
}

func (cu *cacheUnit) LockKey(key interface{}) {
	muItf, _ := cu.keyMap.LoadOrStore(key, &sync.Mutex{})
	mu := muItf.(*sync.Mutex)
	mu.Lock()
}

func (cu *cacheUnit) UnlockKey(key interface{}) {
	// 修复: 只检查已存在的key，避免创建新mutex
	muItf, exists := cu.keyMap.Load(key)
	if !exists {
		return // key不存在说明从未lock过，安全返回
	}
	mu := muItf.(*sync.Mutex)

	// 添加panic恢复机制，防止程序崩溃
	defer func() {
		if r := recover(); r != nil {
			// 静默处理unlock错误，避免程序终止
			return
		}
	}()
	mu.Unlock()
}
