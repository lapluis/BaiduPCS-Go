package pcsclone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

// The resolver constructs account clients itself. A local proxy keeps the
// command path intact while ensuring every request stays in the test fixture.
func linkCommandFixture(t *testing.T, handler http.HandlerFunc, direct, jsonOutput bool) (*commandContext, *bytes.Buffer) {
	t.Helper()
	var flags []string
	if direct {
		flags = append(flags, "--direct")
	}
	if jsonOutput {
		flags = append(flags, "--json")
	}
	return baiduCommandFixture(t, handler, "link", []string{"bd:/fixture"}, flags)
}

func baiduCommandFixture(t *testing.T, handler http.HandlerFunc, name string, args, flagArgs []string) (*commandContext, *bytes.Buffer) {
	t.Helper()
	return commandFixture(t, handler, buildBaiduGroup().Group, name, args, flagArgs)
}

func commandFixture(t *testing.T, handler http.HandlerFunc, commands []*command, name string, args, flagArgs []string) (*commandContext, *bytes.Buffer) {
	t.Helper()
	t.Setenv(EnvConfigFile, "")
	out := &bytes.Buffer{}
	app := &App{CWD: t.TempDir(), Stdout: out, Stderr: &bytes.Buffer{}}
	if err := app.ensureRuntime(filepath.Join(app.CWD, defaultConfigName)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Config.Close() })
	app.Config.EnableHTTPS = false
	app.Config.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
	app.Resolver.Aliases["bd"] = 7
	oldProxy, oldRules := requester.ProxyAddr, requester.ProxyHostnameRules
	t.Cleanup(func() { requester.SetGlobalProxy(oldProxy); requester.SetProxyHostnameRules(oldRules) })
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	requester.SetGlobalProxy(server.URL)
	requester.SetProxyHostnameRules("")
	var cmd *command
	for _, part := range strings.Split(name, "/") {
		cmd = findCommand(commands, part)
		if cmd == nil {
			t.Fatalf("unknown fixture command %q", name)
		}
		commands = cmd.Group
	}
	_, flags, err := parseCommandFlags(flagArgs, cmd.Flags)
	if err != nil {
		t.Fatal(err)
	}
	return &commandContext{app: app, ctx: context.Background(), cmd: cmd, args: args, flags: flags}, out
}

func linkFixtureResponse(w http.ResponseWriter, r *http.Request, directory bool) {
	if r.URL.Query().Get("method") == "meta" {
		isdir := 0
		if directory {
			isdir = 1
		}
		fmt.Fprintf(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","isdir":%d,"size":3}]}`, isdir)
	} else {
		io.WriteString(w, `{"error_code":0,"urls":[{"url":"http://fixture.invalid/file","encrypt":0}]}`)
	}
}

func TestBaiduLinkCancellation(t *testing.T) {
	for _, stage := range []string{"meta", "locatedownload"} {
		for _, phase := range []string{"headers", "body"} {
			for _, direct := range []bool{false, true} {
				if direct && stage == "locatedownload" {
					continue
				}
				t.Run(fmt.Sprintf("%s/%s/direct=%v", stage, phase, direct), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					started, release := make(chan struct{}), make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					var calls atomic.Int32
					c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.URL.Query().Get("method") != stage {
							linkFixtureResponse(w, r, false)
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
					}, direct, true)
					c.ctx = ctx
					done, joined := make(chan error, 1), make(chan struct{})
					go func() { defer close(joined); done <- c.cmd.Run(c) }()
					defer func() { cancel(); unblock(); <-joined }()
					select {
					case <-started:
					case <-time.After(3 * time.Second):
						t.Fatal("request did not reach fixture")
					}
					cancel()
					select {
					case err := <-done:
						if !errors.Is(err, context.Canceled) || out.Len() != 0 {
							t.Fatalf("cancellation lost: err=%v output=%q", err, out.String())
						}
						want := int32(1)
						if stage == "locatedownload" {
							want = 2
						}
						if calls.Load() != want {
							t.Fatalf("unexpected requests: %d", calls.Load())
						}
					case <-time.After(time.Second):
						t.Error("link command ignored cancellation")
					}
				})
			}
		}
	}
}

func TestBaiduLinkOutputAndDirectoryChecks(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, jsonOutput := range []bool{false, true} {
			for _, directory := range []bool{false, true} {
				t.Run(fmt.Sprintf("direct=%v/json=%v/directory=%v", direct, jsonOutput, directory), func(t *testing.T) {
					var requests atomic.Int32
					c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); linkFixtureResponse(w, r, directory) }, direct, jsonOutput)
					err := c.cmd.Run(c)
					calls := requests.Load()
					if directory {
						if err == nil || calls != 1 || out.Len() != 0 {
							t.Fatalf("directory accepted: err=%v calls=%d output=%q", err, calls, out.String())
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					value := strings.TrimSpace(out.String())
					if jsonOutput {
						var result linkResult
						if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.URLs) != 1 {
							t.Fatalf("invalid output: %q (%v)", out.String(), err)
						}
						value = result.URLs[0].URL
					}
					if direct {
						u, err := url.Parse(value)
						if err != nil || u.Query().Get("method") != "download" || u.Query().Get("path") != "/fixture" || calls != 1 {
							t.Fatalf("invalid direct result: %q calls=%d", value, calls)
						}
					} else if value != "http://fixture.invalid/file" || calls != 2 {
						t.Fatalf("invalid locate result: %q calls=%d", value, calls)
					}
				})
			}
		}
	}
}

func TestBaiduLinkAlreadyCanceled(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(fmt.Sprintf("direct=%v", direct), func(t *testing.T) {
			var calls atomic.Int32
			c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				linkFixtureResponse(w, r, false)
			}, direct, false)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			c.ctx = ctx
			if err := c.cmd.Run(c); !errors.Is(err, context.Canceled) || calls.Load() != 0 || out.Len() != 0 {
				t.Fatalf("already canceled command continued: err=%v requests=%d output=%q", err, calls.Load(), out.String())
			}
		})
	}
}

func TestBaiduLinkRejectsWrongMetadataPath(t *testing.T) {
	for _, direct := range []bool{false, true} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("direct=%t/json=%t", direct, jsonOutput), func(t *testing.T) {
				calls := 0
				c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					io.WriteString(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/other","size":3}]}`)
				}, direct, jsonOutput)
				if err := c.cmd.Run(c); err == nil || out.Len() != 0 || calls != 1 {
					t.Fatalf("wrong metadata produced a link: calls=%d output=%q err=%v", calls, out.String(), err)
				}
			})
		}
	}
}

func TestBaiduLinkRejectsMalformedLocateURLs(t *testing.T) {
	for _, list := range []string{
		`[]`, `[null]`, `[{}]`, `[{"url":""}]`,
		`[{"url":"/relative"}]`, `[{"url":"http://:80/file"}]`,
		`[{"url":"ftp://fixture.invalid/file"}]`,
		`[{"url":"http://user:synthetic@fixture.invalid/file"}]`,
		`[{"url":"http://fixture.invalid/%zz"}]`,
		`[{"url":"https://fixture.invalid:65536/file"}]`,
		`[{"url":"https://fixture.invalid:0/file"}]`,
		`[{"url":"http://2001:db8::1/file"}]`,
		`[{"url":"https://fixture.invalid/good"},{"url":"https://fixture.invalid:65536/file"}]`,
		`[{"url":"http://2001:db8::1/file"},{"url":"https://fixture.invalid/good"}]`,
		`[{"url":"http://fixture.invalid/good"},{"url":""}]`,
		`[{"url":""},{"url":"http://fixture.invalid/good"}]`,
	} {
		for _, jsonOutput := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", list, jsonOutput), func(t *testing.T) {
				calls := 0
				c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.URL.Query().Get("method") == "meta" {
						linkFixtureResponse(w, r, false)
						return
					}
					fmt.Fprintf(w, `{"error_code":0,"urls":%s}`, list)
				}, false, jsonOutput)
				if err := c.cmd.Run(c); err == nil || out.Len() != 0 || calls != 2 {
					t.Fatalf("malformed locate list reported success or partial output: calls=%d output=%q err=%v", calls, out.String(), err)
				}
			})
		}
	}
}

func TestBaiduLinkPreservesLocatedURLDetails(t *testing.T) {
	want := linkResult{URLs: []linkView{
		{URL: "http://fixture.invalid:8080/a%2Fb?token=synthetic%2Bvalue&x=1&x=2"},
		{URL: "https://fixture.invalid/encrypted", Encrypt: 1},
		{URL: "//fixture.invalid/relative-scheme?sign=synthetic"},
		{URL: "https://fixture.invalid:65535/file"},
		{URL: "http://[2001:db8::1]:8080/a%2Fb?sign=synthetic%2Bvalue"},
		{URL: "//[fe80::1%25fixture]:8443/file"},
	}}
	for _, jsonOutput := range []bool{false, true} {
		t.Run(fmt.Sprintf("json=%t", jsonOutput), func(t *testing.T) {
			c, out := linkCommandFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("method") == "meta" {
					linkFixtureResponse(w, r, false)
					return
				}
				if err := json.NewEncoder(w).Encode(want); err != nil {
					t.Error(err)
				}
			}, false, jsonOutput)
			if err := c.cmd.Run(c); err != nil {
				t.Fatal(err)
			}
			if jsonOutput {
				var got linkResult
				if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.URLs) != len(want.URLs) {
					t.Fatalf("invalid link output: %q %v", out.String(), err)
				}
				for i := range want.URLs {
					if got.URLs[i] != want.URLs[i] {
						t.Fatalf("link details changed: got=%+v want=%+v", got, want)
					}
				}
			} else {
				var expected strings.Builder
				for _, item := range want.URLs {
					fmt.Fprintln(&expected, item.URL)
				}
				if out.String() != expected.String() {
					t.Fatalf("link order or signed query changed: %q", out.String())
				}
			}
		})
	}
}
