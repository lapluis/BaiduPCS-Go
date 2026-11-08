// Package cachepool []byte缓存池
package cachepool

import (
	"sync"
	"sync/atomic"
)

var (
	// IDCachePool []byte 缓存池
	IDCachePool = cachePool{
		cachepool: sync.Map{},
	}
)

type cachePool struct {
	lastID    int32
	cachepool sync.Map
	growMu    sync.Mutex
}

func (cp *cachePool) Apply(size int) (id int32) {
	cache := RawMallocByteSlice(size)
	for {
		id = atomic.AddInt32(&cp.lastID, 1)
		// Reserve the exact ID returned by the atomic increment. Another
		// caller may advance lastID before this allocation is published.
		if _, loaded := cp.cachepool.LoadOrStore(id, cache); !loaded {
			return id
		}
	}
}

func (cp *cachePool) Existed(id int32) (existed bool) {
	_, existed = cp.cachepool.Load(id)
	return
}

func (cp *cachePool) Get(id int32) []byte {
	cache, ok := cp.cachepool.Load(id)
	if !ok {
		return nil
	}
	return cache.([]byte)
}

func (cp *cachePool) Set(id int32, size int) []byte {
	cache := RawMallocByteSlice(size)
	cp.cachepool.Store(id, cache)
	return cache
}

func (cp *cachePool) SetIfNotExist(id int32, size int) []byte {
	// Serialize conditional growth so a smaller request cannot overwrite a
	// larger allocation after observing the same older buffer.
	cp.growMu.Lock()
	defer cp.growMu.Unlock()
	cache := cp.Get(id)
	if cache == nil || len(cache) < size {
		return cp.Set(id, size)
	}
	return cache
}

func (cp *cachePool) Delete(id int32) {
	cp.cachepool.Delete(id)
}

func (cp *cachePool) DeleteAll() {
	cp.cachepool.Range(func(k interface{}, _ interface{}) bool {
		cp.Delete(k.(int32))
		return true
	})
}

// Apply 申请缓存, 返回缓存id
func Apply(size int) (id int32) {
	return IDCachePool.Apply(size)
}

// Existed 通过缓存id检测是否存在缓存
func Existed(id int32) bool {
	return IDCachePool.Existed(id)
}

// Get 通过缓存id获取缓存[]byte
func Get(id int32) []byte {
	return IDCachePool.Get(id)
}

// Set 设置缓存, 通过给定的缓存id
func Set(id int32, size int) []byte {
	return IDCachePool.Set(id, size)
}

// SetIfNotExist 如果缓存不存在, 则设置缓存池
func SetIfNotExist(id int32, size int) []byte {
	return IDCachePool.SetIfNotExist(id, size)
}

// Delete 通过缓存id删除缓存
func Delete(id int32) {
	IDCachePool.Delete(id)
}

// DeleteAll 清空缓存池
func DeleteAll() {
	IDCachePool.DeleteAll()
}
