package requester

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func portConflict() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connectex", Err: windows.WSAEADDRINUSE}}
}

func TestTCPDialRetriesTemporaryWindowsPortConflicts(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	attempts := 0
	conn, err := dialTCPConnection(context.Background(), func(context.Context, string, string) (net.Conn, error) {
		attempts++
		if attempts < 3 {
			return nil, portConflict()
		}
		return client, nil
	}, "tcp4", "127.0.0.1:1")
	if err != nil || conn != client || attempts != 3 {
		t.Fatalf("temporary conflict did not recover: attempts=%d conn=%v err=%v", attempts, conn != nil, err)
	}
}

func TestTCPDialPortConflictRetriesAreBounded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	attempts := 0
	conn, err := dialTCPConnection(ctx, func(context.Context, string, string) (net.Conn, error) {
		attempts++
		return nil, portConflict()
	}, "tcp4", "127.0.0.1:1")
	if conn != nil || attempts != 5 || !errors.Is(err, windows.WSAEADDRINUSE) {
		t.Fatalf("persistent conflict not bounded: attempts=%d err=%v", attempts, err)
	}
}

func TestTCPDialRetryStopsOnCancellationOrOtherError(t *testing.T) {
	for _, mode := range []string{"canceled", "pre-canceled", "other-error", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := error(context.Canceled)
			other := errors.New("permanent fixture error")
			if mode == "pre-canceled" {
				cancel()
			}
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 5*time.Millisecond)
				defer stop()
				want = context.DeadlineExceeded
			}
			if mode == "other-error" {
				want = other
			}
			attempts := 0
			_, err := dialTCPConnection(ctx, func(context.Context, string, string) (net.Conn, error) {
				attempts++
				if mode == "canceled" {
					cancel()
				}
				if mode == "other-error" {
					return nil, other
				}
				return nil, portConflict()
			}, "tcp4", "127.0.0.1:1")
			wantAttempts := 1
			if mode == "pre-canceled" {
				wantAttempts = 0
			}
			if !errors.Is(err, want) || attempts != wantAttempts {
				t.Fatalf("retry ignored termination: attempts=%d err=%v", attempts, err)
			}
		})
	}
}
