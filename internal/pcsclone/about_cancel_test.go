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

func TestAboutCancellation(t *testing.T) {
	for _, phase := range []string{"before", "headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			c, out := commandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if phase == "before" {
					io.WriteString(w, `{"quota":100,"used":25}`)
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
			}, buildCommands(), "about", []string{"bd:"}, []string{"--json"})
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
					t.Fatal("quota request did not start")
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
					t.Fatalf("quota cancellation lost: err=%v calls=%d output=%q", err, calls.Load(), out.String())
				}
			case <-time.After(time.Second):
				t.Error("about ignored cancellation")
			}
		})
	}
}

func TestAboutOutputAndMalformedResponse(t *testing.T) {
	for _, mode := range []string{"normal", "zero", "missing", "negative-total", "negative-used", "over-quota", "maximum"} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%v", mode, jsonOutput), func(t *testing.T) {
				payload := `{"quota":9007199254740993,"used":25}`
				total, used := int64(9007199254740993), int64(25)
				if mode == "zero" {
					payload = `{"quota":0,"used":0}`
					total, used = 0, 0
				}
				if mode == "missing" {
					payload = `{}`
				}
				if mode == "negative-total" {
					payload = `{"quota":-1,"used":0}`
				}
				if mode == "negative-used" {
					payload = `{"quota":0,"used":-9223372036854775808}`
				}
				if mode == "over-quota" {
					payload = `{"quota":100,"used":125}`
					total, used = 100, 125
				}
				if mode == "maximum" {
					payload = `{"quota":9223372036854775807,"used":0}`
					total, used = 9223372036854775807, 0
				}
				var flags []string
				if jsonOutput {
					flags = append(flags, "--json")
				}
				c, out := commandFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }, buildCommands(), "about", []string{"bd:"}, flags)
				err := c.cmd.Run(c)
				if mode == "missing" || mode == "negative-total" || mode == "negative-used" {
					if err == nil || out.Len() != 0 {
						t.Fatalf("missing quota reported success: err=%v output=%q", err, out.String())
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if jsonOutput {
					var value map[string]int64
					if err := json.Unmarshal(out.Bytes(), &value); err != nil || value["total"] != total || value["used"] != used || value["free"] != total-used {
						t.Fatalf("quota output changed: %q (%v)", out.String(), err)
					}
				} else {
					want := fmt.Sprintf("Total: %d\nUsed: %d\nFree: %d", total, used, total-used)
					if strings.TrimSpace(out.String()) != want {
						t.Fatalf("quota text changed: %q", out.String())
					}
				}
			})
		}
	}
}
