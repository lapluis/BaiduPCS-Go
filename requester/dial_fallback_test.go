package requester

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type addressFixtureResolver struct {
	addrs []string
	calls atomic.Int32
}

func TestTCPDialFallbackDoesNotWaitForFirstAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	exited := make(chan struct{})
	dialer := &net.Dialer{Timeout: 2 * time.Second, ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
		if strings.HasPrefix(address, "127.0.0.2:") {
			defer close(exited)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	conn, err := dialTCPAddresses(context.Background(), dialer, "tcp4", port, []net.IP{net.ParseIP("127.0.0.2"), net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("stalled first address prevented fallback: %v", err)
	}
	conn.Close()
	select {
	case <-exited:
	default:
		t.Fatal("returned before losing dial exited")
	}
}

func TestTCPDialCancellationJoinsActiveAttempts(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	dialer := &net.Dialer{ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
		close(started)
		defer close(exited)
		<-ctx.Done()
		return ctx.Err()
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, err := dialTCPAddresses(ctx, dialer, "tcp4", "1", []net.IP{net.ParseIP("127.0.0.1")})
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
		select {
		case <-exited:
		default:
			t.Fatal("active dial still running after return")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled dial did not return")
	}
}

func TestTCP6DialSkipsIPv4Address(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialTCPAddresses(ctx, &net.Dialer{}, "tcp6", port, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func (r *addressFixtureResolver) LookupHost(context.Context, string) ([]string, error) {
	r.calls.Add(1)
	return r.addrs, nil
}

func (r *addressFixtureResolver) LookupAddr(ctx context.Context, addr string) ([]string, error) {
	return r.LookupHost(ctx, addr)
}

func TestTCPDialUsesAllDNSAddresses(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	before := dnsResolver
	defer func() { dnsResolver = before }()
	for _, tc := range []struct{ name, first, network string }{
		{"address_family", "::1", "tcp4"},
		{"unreachable_first", "127.0.0.2", "tcp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host := tc.name + ".fallback.fixture.invalid"
			defer tcpCache.Delete(host)
			backend := &addressFixtureResolver{addrs: []string{tc.first, "127.0.0.1"}}
			dnsResolver = &serialDNSResolver{resolver: backend, gate: make(chan struct{}, 1)}
			for i := 0; i < 2; i++ {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				conn, err := dialContext(ctx, tc.network, net.JoinHostPort(host, port))
				cancel()
				if err != nil {
					t.Fatalf("reachable second address was not tried: %v", err)
				}
				conn.Close()
			}
			if backend.calls.Load() != 1 {
				t.Fatalf("DNS cache not reused: %d lookups", backend.calls.Load())
			}
		})
	}
}
