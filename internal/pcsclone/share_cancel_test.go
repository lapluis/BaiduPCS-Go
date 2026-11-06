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

func TestShareCommandCancellation(t *testing.T) {
	for _, action := range []string{"create", "list", "cancel"} {
		for _, phase := range []string{"before", "connect"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var calls atomic.Int32
				args := []string{"bd:/fixture"}
				if action == "cancel" {
					args = append(args, "7", "8")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if phase == "connect" {
						close(started)
						select {
						case <-r.Context().Done():
						case <-release:
						}
					}
					http.Error(w, "fixture proxy stopped", http.StatusBadGateway)
				}, "share/"+action, args, []string{"--json"})
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
						t.Fatal("share did not reach proxy")
					}
					cancel()
				}
				select {
				case err := <-done:
					want := int32(1)
					if phase == "before" {
						want = 0
					}
					if !errors.Is(err, context.Canceled) || out.Len() != 0 || calls.Load() != want {
						t.Fatalf("share command cancellation lost: err=%v output=%q calls=%d", err, out.String(), calls.Load())
					}
				case <-time.After(time.Second):
					t.Error("share command ignored cancellation")
				}
			})
		}
	}
}
