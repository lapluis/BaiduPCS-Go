package cachepool

import (
	"math"
	"sync"
	"testing"
)

func TestIDCacheConcurrentApplicationsAreDistinct(t *testing.T) {
	pool := &cachePool{}
	start := make(chan struct{})
	ids := make(chan int32, 1024)
	var wg sync.WaitGroup
	for worker := 0; worker < 64; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 16; i++ {
				ids <- pool.Apply(16)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	seen := make(map[int32]bool)
	for id := range ids {
		if seen[id] {
			t.Errorf("active cache ID allocated twice: %d", id)
		}
		seen[id] = true
		if data := pool.Get(id); len(data) != 16 {
			t.Errorf("allocated buffer lost: id=%d length=%d", id, len(data))
		}
	}
}

func TestIDCacheWrapDoesNotReplaceOccupiedBuffers(t *testing.T) {
	pool := &cachePool{lastID: math.MaxInt32 - 1}
	first := pool.Set(math.MaxInt32, 8)
	second := pool.Set(math.MinInt32, 12)
	first[0], second[0] = 1, 2
	id := pool.Apply(16)
	if id != math.MinInt32+1 || len(pool.Get(id)) != 16 {
		t.Fatalf("wrapped allocation: id=%d length=%d", id, len(pool.Get(id)))
	}
	if data := pool.Get(math.MaxInt32); len(data) != 8 || data[0] != 1 {
		t.Fatal("occupied positive ID replaced")
	}
	if data := pool.Get(math.MinInt32); len(data) != 12 || data[0] != 2 {
		t.Fatal("occupied negative ID replaced")
	}
}
