package pcsclone

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTransferCommandCancellation(t *testing.T) {
	for _, phase := range []string{"before", "connect"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if phase == "connect" {
					close(started)
					select {
					case <-r.Context().Done():
					case <-release:
					}
				}
				http.Error(w, "fixture stopped", http.StatusBadGateway)
			}, "transfer", []string{"https://pan.baidu.com/s/1fixture", "bd:"}, []string{"--code", "abcd", "--json"})
			c.ctx = ctx
			if phase == "before" {
				cancel()
			}
			done, joined := make(chan error, 1), make(chan struct{})
			go func() { defer close(joined); done <- c.cmd.Run(c) }()
			defer func() { cancel(); unblock(); <-joined }()
			if phase == "connect" {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("transfer did not reach proxy")
				}
				cancel()
			}
			select {
			case err := <-done:
				want := int32(1)
				if phase == "before" {
					want = 0
				}
				if !errors.Is(err, context.Canceled) || calls.Load() != want || out.Len() != 0 {
					t.Fatalf("transfer cancellation lost: err=%v requests=%d output=%q", err, calls.Load(), out.String())
				}
			case <-time.After(time.Second):
				t.Error("transfer command ignored cancellation")
			}
		})
	}
}
