package cachemap

import (
	"context"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/expires"
)

type (
	OpFunc          func() expires.DataExpires
	OpFuncWithError func() (expires.DataExpires, error)
)

func (cm *CacheOpMap) CacheOperation(op string, key interface{}, opFunc OpFunc) (data expires.DataExpires) {
	var (
		cache = cm.LazyInitCachePoolOp(op).(*cacheUnit)
		ok    bool
	)

	cache.LockKey(key)
	defer cache.UnlockKey(key)
	revision := cache.currentRevision()
	data, ok = cache.Load(key)
	if !ok {
		data = opFunc()
		if data != nil {
			cache.storeAtRevision(key, data, revision)
		}
		return
	}

	return
}

func (cm *CacheOpMap) CacheOperationWithError(op string, key interface{}, opFunc OpFuncWithError) (data expires.DataExpires, err error) {
	return cm.CacheOperationWithContext(context.Background(), op, key, opFunc)
}

// CacheOperationWithContext allows cancellation while waiting for a key.
// The callback must observe ctx when performing blocking work.
func (cm *CacheOpMap) CacheOperationWithContext(ctx context.Context, op string, key interface{}, opFunc OpFuncWithError) (data expires.DataExpires, err error) {
	var (
		cache = cm.LazyInitCachePoolOp(op).(*cacheUnit)
		ok    bool
	)

	if err = cache.lockKeyContext(ctx, key); err != nil {
		return nil, err
	}
	defer cache.UnlockKey(key)
	revision := cache.currentRevision()
	data, ok = cache.Load(key)
	if !ok {
		data, err = opFunc()
		if err != nil {
			return
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if data == nil {
			// 数据为空时也不存
			return
		}
		cache.storeAtRevision(key, data, revision)
	}

	return
}
