package requester

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestTCPDialHonorsContext(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, cached := range []bool{false, true} {
		address := listener.Addr().String()
		if cached {
			SetTCPHostBind("dial-context-fixture.invalid", "127.0.0.1")
			defer tcpCache.Delete("dial-context-fixture.invalid")
			_, port, _ := net.SplitHostPort(address)
			address = net.JoinHostPort("dial-context-fixture.invalid", port)
		}
		for _, expired := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := error(context.Canceled)
			if expired {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				wantErr = context.DeadlineExceeded
			}
			cancel()
			for _, network := range []string{"tcp", "tcp4"} {
				conn, err := dialContext(ctx, network, address)
				if conn != nil {
					conn.Close()
				}
				if !errors.Is(err, wantErr) {
					t.Errorf("cached=%v expired=%v network=%s: got %v, want %v", cached, expired, network, err, wantErr)
				}
			}
		}
	}
}

func TestTCPDialPreservesHostBinding(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	SetTCPHostBind("dial-binding-fixture.invalid", "127.0.0.1")
	defer tcpCache.Delete("dial-binding-fixture.invalid")
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialContext(ctx, "tcp4", net.JoinHostPort("dial-binding-fixture.invalid", port))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestTCPDialCancellationWhileWaitingForCache(t *testing.T) {
	host := "dial-cache-wait-fixture.invalid"
	SetTCPHostBind(host, "127.0.0.1")
	defer tcpCache.Delete(host)
	tcpCache.LockKey(host)
	defer tcpCache.UnlockKey(host)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := dialContext(ctx, "tcp", net.JoinHostPort(host, "1"))
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial ignored cancellation while waiting for host cache")
	}
}

func TestResolveTCPHostCancellationWhileWaitingForDNS(t *testing.T) {
	gate := dnsResolver.gate
	gate <- struct{}{}
	defer func() { <-gate }()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := resolveTCPHost(ctx, "resolve-wait-fixture.invalid"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("DNS ignored cancellation while waiting for lookup slot")
	}
}
