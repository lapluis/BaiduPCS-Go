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

func TestBaiduSearchCancellation(t *testing.T) {
	for _, phase := range []string{"before", "headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if phase == "before" {
					io.WriteString(w, `{"error_code":0,"list":[]}`)
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
			}, "search", []string{"bd:/fixture", "needle"}, []string{"--json"})
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
					t.Fatal("search did not reach fixture")
				}
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || out.Len() != 0 {
					t.Fatalf("cancellation lost: err=%v output=%q", err, out.String())
				}
				want := int32(1)
				if phase == "before" {
					want = 0
				}
				if calls.Load() != want {
					t.Fatalf("unexpected requests: %d", calls.Load())
				}
			case <-time.After(time.Second):
				t.Error("search ignored cancellation")
			}
		})
	}
}

func TestBaiduSearchPreservesOutputAndQuery(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("recursive=%v/json=%v", recursive, jsonOutput), func(t *testing.T) {
				var flags []string
				if recursive {
					flags = append(flags, "--recursive")
				}
				if jsonOutput {
					flags = append(flags, "--json")
				}
				c, out := baiduCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					want := "0"
					if recursive {
						want = "1"
					}
					q := r.URL.Query()
					if q.Get("method") != "search" || q.Get("path") != "/fixture" || q.Get("wd") != "中文 + needle" || q.Get("re") != want {
						t.Errorf("incorrect search query: %s", r.URL)
					}
					io.WriteString(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture/one","server_filename":"one","size":3},{"fs_id":2,"server_filename":"dir","isdir":1}]}`)
				}, "search", []string{"bd:/fixture", "中文 + needle"}, flags)
				if err := c.cmd.Run(c); err != nil {
					t.Fatal(err)
				}
				if jsonOutput {
					var views []searchView
					if err := json.Unmarshal(out.Bytes(), &views); err != nil || len(views) != 2 {
						t.Fatalf("invalid JSON output: %q (%v)", out.String(), err)
					}
					if views[0].Path != "/fixture/one" || views[0].Size != 3 || views[1].Path != "/fixture/dir" || !views[1].IsDir {
						t.Fatalf("incorrect output: %+v", views)
					}
				} else if strings.TrimSpace(out.String()) != "/fixture/one\n/fixture/dir/" {
					t.Fatalf("incorrect text output: %q", out.String())
				}
			})
		}
	}
}
