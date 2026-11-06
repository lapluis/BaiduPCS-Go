package rc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func quotaCommandFixture(t *testing.T, handler http.HandlerFunc) (*Resolver, *bytes.Buffer) {
	t.Helper()
	previous := pcsconfig.Config
	cfg := pcsconfig.NewConfig(filepath.Join(t.TempDir(), "config.json"))
	cfg.AppID = 1
	cfg.PCSAddr = "pcs.baidu.com"
	cfg.EnableHTTPS = false
	cfg.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
	pcsconfig.Config = cfg
	t.Cleanup(func() { pcsconfig.Config = previous })
	oldProxy, oldRules := requester.ProxyAddr, requester.ProxyHostnameRules
	t.Cleanup(func() { requester.SetGlobalProxy(oldProxy); requester.SetProxyHostnameRules(oldRules) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	requester.SetGlobalProxy(server.URL)
	requester.SetProxyHostnameRules("")
	return &Resolver{CWD: t.TempDir(), Aliases: map[string]uint64{"bd": 7}}, &bytes.Buffer{}
}

func TestLegacyQuotaOutputAndMalformedResponse(t *testing.T) {
	for _, mode := range []string{"normal", "zero", "missing"} {
		t.Run(mode, func(t *testing.T) {
			payload := `{"quota":9007199254740993,"used":25}`
			total, used := int64(9007199254740993), int64(25)
			if mode == "zero" {
				payload = `{"quota":0,"used":0}`
				total, used = 0, 0
			}
			if mode == "missing" {
				payload = `{}`
			}
			r, out := quotaCommandFixture(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) })
			err := runCommand(context.Background(), "quota", parsed{Args: []string{"bd:"}}, r, out)
			if mode == "missing" {
				if err == nil || out.Len() != 0 {
					t.Fatalf("legacy quota missing response accepted: err=%v output=%q", err, out.String())
				}
				return
			}
			var result map[string]int64
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["total"] != total || result["used"] != used || result["free"] != total-used {
				t.Fatalf("legacy output changed: %q (%v)", out.String(), err)
			}
		})
	}
}

func TestLegacyQuotaCancellation(t *testing.T) {
	for _, phase := range []string{"before", "headers", "body"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			r, out := quotaCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
			})
			if phase == "before" {
				cancel()
			}
			done, joined := make(chan error, 1), make(chan struct{})
			go func() { defer close(joined); done <- runCommand(ctx, "quota", parsed{Args: []string{"bd:"}}, r, out) }()
			defer func() { cancel(); unblock(); <-joined }()
			if phase != "before" {
				select {
				case <-started:
				case <-time.After(3 * time.Second):
					t.Fatal("legacy quota did not start")
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
					t.Fatalf("legacy quota cancellation lost: err=%v calls=%d output=%q", err, calls.Load(), out.String())
				}
			case <-time.After(time.Second):
				t.Error("legacy quota ignored cancellation")
			}
		})
	}
}
