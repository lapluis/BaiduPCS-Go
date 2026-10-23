package requester

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type controlledDNSResolver struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *controlledDNSResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	r.calls.Add(1)
	if host == "panic.fixture.invalid" {
		panic("fixture panic")
	}
	if host == "blocked.fixture.invalid" {
		close(r.started)
		<-r.release
	}
	if host == "cancel.fixture.invalid" {
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []string{"127.0.0.1"}, nil
}

func (r *controlledDNSResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return r.LookupHost(ctx, addr)
}

func TestDNSCancellationKeepsActualLookupsSerialized(t *testing.T) {
	backend := &controlledDNSResolver{started: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(backend.release)
		}
	}()
	resolver := &serialDNSResolver{resolver: backend, gate: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := resolver.LookupHost(ctx, "blocked.fixture.invalid"); done <- err }()
	<-backend.started
	cancel()
	waitCtx, stopWait := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stopWait()
	if _, err := resolver.LookupHost(waitCtx, "waiting.fixture.invalid"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiter: %v", err)
	}
	if backend.calls.Load() != 1 {
		t.Fatalf("started another lookup before old lookup exited: %d", backend.calls.Load())
	}
	close(backend.release)
	released = true
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller did not return after backend exited")
	}
	freshCtx, stopFresh := context.WithTimeout(context.Background(), time.Second)
	defer stopFresh()
	if _, err := resolver.LookupHost(freshCtx, "fresh.fixture.invalid"); err != nil {
		t.Fatal(err)
	}
	if backend.calls.Load() != 2 {
		t.Fatalf("canceled waiter started a lookup: calls=%d", backend.calls.Load())
	}
}

func TestDNSPassesCancellationToActiveLookup(t *testing.T) {
	backend := &controlledDNSResolver{started: make(chan struct{})}
	resolver := &serialDNSResolver{resolver: backend, gate: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := resolver.LookupHost(ctx, "cancel.fixture.invalid"); done <- err }()
	<-backend.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active lookup did not receive cancellation")
	}
}

func TestDNSPanicIsRecoveredAndReleasesSlot(t *testing.T) {
	backend := &controlledDNSResolver{}
	resolver := &serialDNSResolver{resolver: backend, gate: make(chan struct{}, 1)}
	for _, reverse := range []bool{false, true} {
		lookup := resolver.LookupHost
		if reverse {
			lookup = resolver.LookupAddr
		}
		_, err := lookup(context.Background(), "panic.fixture.invalid")
		if err == nil || !strings.Contains(err.Error(), "fixture panic") {
			t.Fatalf("panic not converted to error: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = lookup(ctx, "fresh.fixture.invalid")
		cancel()
		if err != nil {
			t.Fatalf("panic stranded lookup slot: %v", err)
		}
	}
}
