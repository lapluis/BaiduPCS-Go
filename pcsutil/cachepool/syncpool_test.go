package cachepool

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestBufferPoolDefaultAndExplicitSizes(t *testing.T) {
	previous := int(atomic.LoadInt64(&syncPoolSize))
	defer SetSyncPoolSize(previous)
	for _, size := range []int{128, 8192, 0, -1} {
		SetSyncPoolSize(size)
		want := size
		if want <= 0 {
			want = 64 << 10
		}
		defaultBuffer := GetBuffer(0)
		explicit := GetBuffer(1024)
		if len(defaultBuffer) != want || len(explicit) != 1024 {
			t.Fatalf("wrong buffers after default %d: default=%d explicit=%d", size, len(defaultBuffer), len(explicit))
		}
		PutBuffer(defaultBuffer)
		PutBuffer(explicit)
	}
	PutBuffer(nil)
}

func TestBufferPoolConcurrentSizesAndOwnership(t *testing.T) {
	previous := int(atomic.LoadInt64(&syncPoolSize))
	defer SetSyncPoolSize(previous)
	SetSyncPoolSize(128)
	sizes := []int{1, 1024, 4096, 8192}
	var wg sync.WaitGroup
	for id := 0; id < 12; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if id == 0 {
					SetSyncPoolSize(sizes[1+i%2])
				}
				size := sizes[id%len(sizes)]
				buf := GetBuffer(size)
				if len(buf) != size || cap(buf) != size {
					t.Errorf("buffer size changed: len=%d cap=%d want=%d", len(buf), cap(buf), size)
					return
				}
				buf[0] = byte(id)
				runtime.Gosched()
				if buf[0] != byte(id) {
					t.Error("buffer shared by active borrowers")
				}
				PutBuffer(buf)
			}
		}(id)
	}
	wg.Wait()
}
