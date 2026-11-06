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

func TestRecycleCommandCancellation(t *testing.T) {
	for _, action := range []string{"list", "restore", "delete", "clear"} {
		for _, phase := range []string{"before", "headers", "body"} {
			t.Run(action+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started, release := make(chan struct{}), make(chan struct{})
				unblock := sync.OnceFunc(func() { close(release) })
				var calls atomic.Int32
				args := []string{"bd:"}
				if action == "restore" || action == "delete" {
					args = append(args, "7", "8")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if phase == "before" {
						io.WriteString(w, `{"errno":0,"error_code":0,"list":[],"extra":{"list":[],"succNum":0}}`)
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
				}, "recycle/"+action, args, []string{"--json"})
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
						t.Fatal("recycle request did not start")
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
					t.Error("recycle request ignored cancellation")
				}
			})
		}
	}
}

func TestRecycleCommandOutput(t *testing.T) {
	for _, action := range []string{"list", "restore", "delete", "clear"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", action, jsonOutput), func(t *testing.T) {
				args := []string{"bd:"}
				if action == "restore" || action == "delete" {
					args = append(args, "7", "8")
				}
				var flags []string
				if jsonOutput {
					flags = append(flags, "--json")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					io.WriteString(w, `{"errno":0,"error_code":0,"list":[{"fs_id":7,"path":"/fixture","size":9,"leftTime":5}],"extra":{"list":[{"fs_id":7},{"fs_id":8}],"succNum":3}}`)
				}, "recycle/"+action, args, flags)
				if err := c.cmd.Run(c); err != nil {
					t.Fatal(err)
				}
				if jsonOutput {
					if action == "list" {
						var files []recycleView
						if err := json.Unmarshal(out.Bytes(), &files); err != nil || len(files) != 1 {
							t.Fatalf("invalid listing: %q (%v)", out.String(), err)
						}
						if files[0].FsID != 7 || files[0].Path != "/fixture" || files[0].Size != 9 || files[0].LeftTime != 5 {
							t.Fatalf("invalid file: %+v", files[0])
						}
					} else {
						want := map[string]string{"restore": "{\"restored\":[7,8]}", "delete": "{\"deleted\":[7,8]}", "clear": "{\"cleared\":3}"}[action]
						if strings.TrimSpace(out.String()) != want {
							t.Fatalf("unexpected output: %q want %q", out.String(), want)
						}
					}
				} else {
					want := map[string]string{"list": "fs_id=7 size=9 left_time=5 path=/fixture", "restore": "Restored 2 item(s)", "delete": "Permanently deleted 2 item(s)", "clear": "Emptied recycle bin, 3 item(s)"}[action]
					if strings.TrimSpace(out.String()) != want {
						t.Fatalf("unexpected output: %q want %q", out.String(), want)
					}
				}
			})
		}
	}
}
