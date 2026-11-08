package cachepool

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestIDCacheConcurrentSetReturnsOwnAllocation(t *testing.T) {
	pool := &cachePool{}
	var wrong atomic.Int64
	runCacheCallers(32, func(worker int) {
		for i := 0; i < 256; i++ {
			if data := pool.Set(1, worker+1); len(data) != worker+1 {
				wrong.Add(1)
			}
		}
	})
	if count := wrong.Load(); count != 0 {
		t.Fatalf("Set returned another operation's buffer %d times", count)
	}
}

func TestIDCacheConcurrentReadAndDeleteDoesNotPanic(t *testing.T) {
	pool := &cachePool{}
	var panics atomic.Int64
	runCacheCallers(32, func(_ int) {
		defer func() {
			if recover() != nil {
				panics.Add(1)
			}
		}()
		for i := 0; i < 256; i++ {
			pool.Set(1, 16)
			pool.Delete(1)
			pool.Get(1) // A concurrent deletion may produce nil, never panic.
		}
	})
	if count := panics.Load(); count != 0 {
		t.Fatalf("read/delete panicked in %d callers", count)
	}
}

func TestIDCacheConcurrentGrowthPreservesLargestRequest(t *testing.T) {
	for round := 0; round < 16; round++ {
		pool := &cachePool{}
		var wrong, panics atomic.Int64
		runCacheCallers(32, func(worker int) {
			defer func() {
				if recover() != nil {
					panics.Add(1)
				}
			}()
			for i := 0; i < 16; i++ {
				if data := pool.SetIfNotExist(1, worker+1); len(data) < worker+1 {
					wrong.Add(1)
				}
			}
		})
		if wrong.Load() != 0 || panics.Load() != 0 || len(pool.Get(1)) != 32 {
			t.Fatalf("growth lost: wrong=%d panics=%d final=%d", wrong.Load(), panics.Load(), len(pool.Get(1)))
		}
	}
}

func TestIDCacheZeroLengthLifecycle(t *testing.T) {
	pool := &cachePool{}
	data := pool.SetIfNotExist(1, 0)
	if data == nil || len(data) != 0 || !pool.Existed(1) {
		t.Fatal("zero-length allocation was not stored")
	}
	pool.DeleteAll()
	if pool.Existed(1) || pool.Get(1) != nil {
		t.Fatal("deleted cache remained visible")
	}
}

func runCacheCallers(count int, run func(int)) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < count; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			run(worker)
		}(worker)
	}
	close(start)
	wg.Wait()
}
