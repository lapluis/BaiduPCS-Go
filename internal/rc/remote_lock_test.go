package rc

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRemoteMutexWaitersCancelWithoutLosingLock(t *testing.T) {
	var m remoteMutex
	m.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() {
			err := m.LockContext(ctx)
			if err == nil {
				m.Unlock()
			}
			done <- err
		}()
	}
	cancel()
	for i := 0; i < 32; i++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			m.Unlock()
			t.Fatal("waiter ignored cancellation")
		}
	}
	m.Unlock()
	for i := 0; i < 20; i++ {
		m.Lock()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			err := m.LockContext(ctx)
			if err == nil {
				m.Unlock()
			}
			done <- err
		}()
		cancel()
		m.Unlock()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancel/release race stranded waiter")
		}
		check, stop := context.WithTimeout(context.Background(), time.Second)
		err := m.LockContext(check)
		stop()
		if err != nil {
			t.Fatal("lock token leaked", err)
		}
		m.Unlock()
	}
}

func TestRemoteMutexDeadlineAndPrecanceledWaits(t *testing.T) {
	var m remoteMutex
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.LockContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	m.Lock()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if err := m.LockContext(ctx); !errors.Is(err, context.DeadlineExceeded) {
		m.Unlock()
		t.Fatal(err)
	}
	m.Unlock()
	check, finish := context.WithTimeout(context.Background(), time.Second)
	defer finish()
	if err := m.LockContext(check); err != nil {
		t.Fatal("timeout consumed lock", err)
	}
	m.Unlock()
}

func TestRemoteMutexSerializesConcurrentClientWork(t *testing.T) {
	var m remoteMutex
	var active atomic.Int32
	var overlap atomic.Bool
	var wg sync.WaitGroup
	count := 0
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				m.Lock()
				if active.Add(1) != 1 {
					overlap.Store(true)
				}
				runtime.Gosched()
				count++
				active.Add(-1)
				m.Unlock()
			}
		}()
	}
	wg.Wait()
	if overlap.Load() || count != 320 {
		t.Fatal("client work overlapped or lost updates", count)
	}
}
