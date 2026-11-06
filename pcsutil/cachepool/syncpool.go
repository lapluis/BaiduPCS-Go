package cachepool

import (
	"sync"
	"sync/atomic"
)

var (
	syncPoolSize int64    = 64 << 10
	sizedPools   sync.Map // map[int]*sync.Pool
	// SyncPool retains the legacy untyped pool. Sized users should use GetBuffer.
	SyncPool = sync.Pool{
		New: func() interface{} {
			return RawMallocByteSlice(int(atomic.LoadInt64(&syncPoolSize)))
		},
	}
)

// SetSyncPoolSize sets the default for new legacy buffers and GetBuffer(0).
// Explicitly sized buffers remain independent of this default.
func SetSyncPoolSize(size int) {
	if size <= 0 {
		size = 64 << 10
	}
	atomic.StoreInt64(&syncPoolSize, int64(size))
}

func poolForSize(size int) *sync.Pool {
	if pool, ok := sizedPools.Load(size); ok {
		return pool.(*sync.Pool)
	}
	pool, _ := sizedPools.LoadOrStore(size, &sync.Pool{
		New: func() interface{} { return RawMallocByteSlice(size) },
	})
	return pool.(*sync.Pool)
}

// GetBuffer returns a reusable buffer of exactly size bytes. Non-positive sizes
// use the configured default. Different sizes never share a pool.
func GetBuffer(size int) []byte {
	if size <= 0 {
		size = int(atomic.LoadInt64(&syncPoolSize))
	}
	return poolForSize(size).Get().([]byte)
}

// PutBuffer releases a buffer acquired with GetBuffer.
func PutBuffer(buf []byte) {
	if cap(buf) > 0 {
		poolForSize(cap(buf)).Put(buf[:cap(buf)])
	}
}
