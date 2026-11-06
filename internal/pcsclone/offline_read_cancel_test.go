package pcsclone

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOfflineReadCancellation(t *testing.T) {
	for _, action := range []string{"list", "query"} {
		for _, phase := range []string{"before", "headers", "body"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var calls atomic.Int32
				args := []string{"bd:"}
				if action == "query" {
					args = append(args, "1")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if phase == "before" {
						io.WriteString(w, `{"error_code":0}`)
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
				if phase == "before" {
					cancel()
				}
				done, joined := make(chan error, 1), make(chan struct{})
				go func() { defer close(joined); done <- c.cmd.Run(c) }()
				defer func() { cancel(); unblock(); <-joined }()
				if phase != "before" {
					select {
					case <-started:
					case <-time.After(3 * time.Second):
						t.Fatal("offline request did not start")
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
						t.Fatalf("cancellation lost: err=%v output=%q requests=%d", err, out.String(), calls.Load())
					}
				case <-time.After(time.Second):
					t.Error("offline read ignored cancellation")
				}
			})
		}
	}
}

func TestOfflineReadOutput(t *testing.T) {
	for _, action := range []string{"list", "query"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", action, jsonOutput), func(t *testing.T) {
				args := []string{"bd:"}
				if action == "query" {
					args = append(args, "7")
				}
				var flags []string
				if jsonOutput {
					flags = append(flags, "--json")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					if action == "list" {
						io.WriteString(w, `{"error_code":0,"task_info":[{"task_id":"7","status":"1","task_name":"fixture","file_size":"64","finished_size":"16"}]}`)
					} else {
						io.WriteString(w, `{"error_code":0,"task_info":{"7":{"status":"1","task_name":"fixture","file_size":"64","finished_size":"16"}}}`)
					}
				}, "offline/"+action, args, flags)
				if err := c.cmd.Run(c); err != nil {
					t.Fatal(err)
				}
				if jsonOutput {
					var views []offlineTaskView
					if err := json.Unmarshal(out.Bytes(), &views); err != nil || len(views) != 1 {
						t.Fatalf("invalid output: %q (%v)", out.String(), err)
					}
					if views[0].TaskID != 7 || views[0].Size != 64 || views[0].Finished != 16 || views[0].Name != "fixture" {
						t.Fatalf("incorrect output: %+v", views)
					}
				} else if !strings.Contains(out.String(), "task_id=7") || !strings.Contains(out.String(), "name=fixture") {
					t.Fatalf("incorrect text output: %q", out.String())
				}
			})
		}
	}
}
