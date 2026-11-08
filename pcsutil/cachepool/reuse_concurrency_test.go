package cachepool

import (
	"sync"
	"testing"
)

func TestCacheConcurrentAcquireAndRelease(t *testing.T) {
	pool := &cachePool2{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 128; i++ {
				cache := pool.Require(16)
				if len(cache.Bytes()) < 16 {
					t.Error("active allocation was released by another caller")
				}
				cache.Free()
			}
		}()
	}
	close(start)
	wg.Wait()
	pool.DeleteNotUsed()
	for _, cache := range pool.pool {
		if cache != nil {
			t.Fatal("released cache retained after cleanup")
		}
	}
}
