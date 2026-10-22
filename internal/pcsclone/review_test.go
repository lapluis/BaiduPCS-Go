package pcsclone

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type reviewTransport func(*http.Request) (*http.Response, error)

func (f reviewTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStandaloneDisablesLegacyVerbose(t *testing.T) {
	oldVerbose, oldOutputs := pcsverbose.IsVerbose, pcsverbose.Outputs
	t.Cleanup(func() {
		pcsverbose.IsVerbose, pcsverbose.Outputs = oldVerbose, oldOutputs
	})
	var raw bytes.Buffer
	pcsverbose.IsVerbose = true // Simulate inherited BAIDUPCS_GO_VERBOSE=1.
	pcsverbose.Outputs = []io.Writer{&raw}
	requireCode(t, 0, "", "help")
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = reviewTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r,
			Body: io.NopCloser(strings.NewReader(`{"quota":100,"used":25}`))}, nil
	})
	pcs := baidupcs.NewPCSWithClient(1, client)
	if _, _, err := pcs.QuotaInfo(); err != nil {
		t.Fatal(err)
	}
	if raw.Len() != 0 {
		t.Fatal("standalone entry left legacy request logging enabled")
	}
}

func TestImportDoesNotInitializeEmptyLegacySource(t *testing.T) {
	legacy := filepath.Join(t.TempDir(), defaultConfigName)
	if err := os.WriteFile(legacy, nil, 0600); err != nil {
		t.Fatal(err)
	}
	requireCode(t, 1, legacy, "config", "import-pcsgo", "--from", legacy)
	data, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Fatal("import initialization rewrote the empty legacy source")
	}
}

func TestOfflineBatchStopsAfterCancellation(t *testing.T) {
	for _, action := range []string{"add", "cancel", "delete"} {
		t.Run(action, func(t *testing.T) {
			oldEnv, hadEnv := os.LookupEnv(EnvConfigFile)
			defer func() {
				if hadEnv {
					_ = os.Setenv(EnvConfigFile, oldEnv)
				} else {
					_ = os.Unsetenv(EnvConfigFile)
				}
			}()
			app := &App{CWD: t.TempDir(), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
			if err := app.ensureRuntime(filepath.Join(app.CWD, defaultConfigName)); err != nil {
				t.Fatal(err)
			}
			defer pcsconfig.Config.Close()
			oldProxy, oldRules := requester.ProxyAddr, requester.ProxyHostnameRules
			defer func() {
				requester.SetGlobalProxy(oldProxy)
				requester.SetProxyHostnameRules(oldRules)
			}()
			pcsconfig.Config.EnableHTTPS = false
			pcsconfig.Config.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
			app.Resolver.Aliases["bd"] = 7
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				cancel()
				_, _ = io.WriteString(w, `{"error_code":0,"task_id":1}`)
			}))
			defer server.Close()
			requester.SetGlobalProxy(server.URL)
			requester.SetProxyHostnameRules("")
			args := []string{"bd:", "1", "2", "3"}
			if action == "add" {
				args = []string{"bd:", "http://fixture.invalid/1", "http://fixture.invalid/2", "http://fixture.invalid/3"}
			}
			cmd := findCommand(findCommand(buildBaiduGroup().Group, "offline").Group, action)
			_, flags, err := parseCommandFlags(nil, cmd.Flags)
			if err != nil {
				t.Fatal(err)
			}
			err = cmd.Run(&commandContext{app: app, ctx: ctx, cmd: cmd, args: args, flags: flags})
			if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
				t.Fatalf("canceled batch made %d requests; error = %v", calls.Load(), err)
			}
		})
	}
}

func TestImportRejectsSharedLegacyStorage(t *testing.T) {
	for _, sameConfig := range []bool{false, true} {
		name := "shared-directory"
		if sameConfig {
			name = "same-config-file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			legacy := filepath.Join(dir, defaultConfigName)
			aliases := filepath.Join(dir, "rc_remotes.json")
			beforeConfig := []byte(`{"baidu_user_list":[{"uid":7,"name":"seven","bduss":"SYNTHETIC"}]}`)
			beforeAliases := []byte("{\n  \"old\": 7\n}\n")
			for path, data := range map[string][]byte{legacy: beforeConfig, aliases: beforeAliases} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := filepath.Join(dir, "pcsclone.json")
			if sameConfig {
				config = legacy
			}
			_, stderr := requireCode(t, 1, config, "config", "import-pcsgo", "--from", legacy)
			if !strings.Contains(stderr, "separate") {
				t.Fatalf("missing isolation guidance: %s", stderr)
			}
			for path, before := range map[string][]byte{legacy: beforeConfig, aliases: beforeAliases} {
				after, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatalf("import changed legacy storage: %s", filepath.Base(path))
				}
			}
		})
	}
}
