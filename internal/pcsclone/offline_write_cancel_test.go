package pcsclone

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOfflineWriteCancellation(t *testing.T) {
	for _, action := range []string{"add", "cancel", "delete", "clear"} {
		for _, phase := range []string{"headers", "body", "after-success"} {
			if phase == "after-success" && action == "clear" {
				continue
			}
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var calls atomic.Int32
				args := []string{"bd:"}
				switch action {
				case "add":
					args = append(args, "http://fixture.invalid/one", "http://fixture.invalid/two")
					if phase == "after-success" {
						args = append(args, "http://fixture.invalid/three")
					}
				case "cancel", "delete":
					args = append(args, "1", "2")
					if phase == "after-success" {
						args = append(args, "3")
					}
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					count := calls.Add(1)
					if phase == "after-success" && count == 1 {
						io.WriteString(w, `{"error_code":0,"task_id":7}`)
						return
					}
					if phase == "body" {
						io.WriteString(w, "{")
						w.(http.Flusher).Flush()
					}
					close(started)
					select {
					case <-r.Context().Done():
					case <-release:
					}
				}, "offline/"+action, args, []string{"--json"})
				c.ctx = ctx
				done, joined := make(chan error, 1), make(chan struct{})
				go func() { defer close(joined); done <- c.cmd.Run(c) }()
				defer func() { cancel(); unblock(); <-joined }()
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("offline write did not start")
				}
				cancel()
				select {
				case err := <-done:
					wantCalls, wantSuccess := int32(1), 0
					if phase == "after-success" {
						wantCalls, wantSuccess = 2, 1
					}
					if !errors.Is(err, context.Canceled) || calls.Load() != wantCalls {
						t.Fatalf("cancellation lost: err=%v requests=%d", err, calls.Load())
					}
					if action == "clear" {
						if out.Len() != 0 {
							t.Fatalf("clear reported success: %q", out.String())
						}
						return
					}
					if action == "add" {
						var result offlineAddResult
						if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Added) != wantSuccess || len(result.Errors) != 1 {
							t.Fatalf("incorrect canceled add summary: %q (%v)", out.String(), err)
						}
					} else {
						var result struct {
							Succeeded []int64 `json:"succeeded"`
							Failed    int     `json:"failed"`
						}
						if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Succeeded) != wantSuccess || result.Failed != 1 {
							t.Fatalf("incorrect canceled action summary: %q (%v)", out.String(), err)
						}
					}
				case <-time.After(time.Second):
					t.Error("offline write ignored cancellation")
				}
			})
		}
	}
}
