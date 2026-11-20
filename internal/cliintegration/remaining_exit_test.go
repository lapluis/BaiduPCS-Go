package cliintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyLocateShareTransferAndSumFailures(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "remaining.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	const digest = "e2fc714c4727ee9395f324cd2e7f331f"
	for _, op := range []string{"locate", "share", "transfer", "sum"} {
		states := []string{"success", "remote", "http", "malformed", "empty-links", "partial", "glob-empty"}
		switch op {
		case "share":
			states = []string{"proxy", "glob-empty"}
		case "transfer":
			states = []string{"proxy", "invalid"}
		case "sum":
			states = []string{"success", "missing", "mixed"}
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method == http.MethodConnect {
						http.Error(w, "fixture refuses external tunnel", 403)
						return
					}
					if state == "glob-empty" {
						if r.URL.Path != "/api/list" {
							t.Errorf("unexpected glob request: %s", r.URL)
						}
						w.Write([]byte(`{"errno":0,"list":[]}`))
						return
					}
					if op != "locate" || r.URL.Query().Get("method") != "locatedownload" {
						t.Errorf("unexpected request: %s", r.URL)
						http.Error(w, "bad request", 400)
						return
					}
					if state == "http" {
						http.Error(w, "fixture unavailable", 503)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if state == "malformed" {
						w.Write([]byte(`{`))
						return
					}
					if state == "remote" || state == "partial" && r.URL.Query().Get("path") == "/bad" {
						w.Write([]byte(`{"error_code":31045}`))
						return
					}
					if state == "empty-links" {
						w.Write([]byte(`{"error_code":0,"urls":[]}`))
						return
					}
					w.Write([]byte(`{"error_code":0,"urls":[{"url":"http://download.fixture.invalid/file","encrypt":0}]}`))
				}))
				defer server.Close()
				dir := t.TempDir()
				data, err := json.Marshal(map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]interface{}{{"uid": 7, "workdir": "/"}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				good, missing := filepath.Join(dir, "good.bin"), filepath.Join(dir, "missing.bin")
				if err := os.WriteFile(good, []byte("abcd"), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{"locate", "/fixture"}
				wantCalls := int32(1)
				switch op {
				case "locate":
					if state == "partial" {
						args = []string{"locate", "/bad", "/good"}
						wantCalls = 2
					}
					if state == "glob-empty" {
						args = []string{"locate", "/fixture/*"}
					}
				case "share":
					args = []string{"share", "set", "/fixture"}
					if state == "glob-empty" {
						args = []string{"share", "set", "/fixture/*"}
					}
				case "transfer":
					args = []string{"transfer", "https://pan.baidu.com/s/1fixture?pwd=Ab12"}
					if state == "invalid" {
						args = []string{"transfer", "https://foreign.invalid/s/1fixture?pwd=Ab12"}
						wantCalls = 0
					}
				case "sum":
					wantCalls = 0
					args = []string{"sumfile", good}
					if state == "missing" {
						args = []string{"sumfile", missing}
					}
					if state == "mixed" {
						args = []string{"sumfile", missing, good}
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir = dir
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
						cmd.Env = append(cmd.Env, entry)
					}
				}
				cmd.Env = childRaceEnv(t, append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+dir))
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				err = cmd.Run()
				if ctx.Err() != nil {
					t.Fatalf("timeout: %v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
				}
				if calls.Load() != wantCalls {
					t.Errorf("requests=%d want=%d", calls.Load(), wantCalls)
				}
				if state == "success" {
					marker := "download.fixture.invalid"
					if op == "sum" {
						marker = digest
					}
					if err != nil || !strings.Contains(stdout.String(), marker) {
						t.Errorf("valid command failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || strings.Contains(stderr.String(), "panic:") {
						t.Errorf("failure returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if state == "partial" && (!strings.Contains(stdout.String(), "download.fixture.invalid") || !strings.Contains(stderr.String(), "/bad")) {
						t.Errorf("partial links or failed path lost: stdout=%s stderr=%s", stdout.String(), stderr.String())
					}
					if state == "mixed" && !strings.Contains(stdout.String(), digest) {
						t.Errorf("later checksum lost: %s", stdout.String())
					}
				}
			})
		}
	}
}

func TestLegacyRemainingUtilityExitStatus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pcs-utility-test.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	assertCLIRaceBuild(t, binary, false)

	runCLI := func(t *testing.T, configDir string, args ...string) (string, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = configDir
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = childRaceEnv(t, append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+configDir))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			t.Fatalf("timeout: %v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
		}
		return stdout.String(), stderr.String(), err
	}

	t.Run("setastoken", func(t *testing.T) {
		t.Run("no active account", func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "pcs_config.json")
			initial := []byte(`{"appid":266719}`)
			if err := os.WriteFile(cfgPath, initial, 0600); err != nil {
				t.Fatal(err)
			}
			_, stderr, err := runCLI(t, dir, "setastoken", "tok-fixture")
			if _, ok := err.(*exec.ExitError); !ok || stderr == "" {
				t.Fatalf("expected failure, got err=%v stderr=%q", err, stderr)
			}
			got, readErr := os.ReadFile(cfgPath)
			if readErr != nil || !bytes.Equal(got, initial) {
				t.Fatalf("config modified on failure: %v %s", readErr, got)
			}
		})

		t.Run("missing and extra args", func(t *testing.T) {
			for _, args := range [][]string{{"setastoken"}, {"setastoken", "a", "b"}} {
				dir := t.TempDir()
				cfgPath := filepath.Join(dir, "pcs_config.json")
				initial := []byte(`{"appid":1,"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"name":"alice","workdir":"/","access_token":"old-tok"}]}`)
				if err := os.WriteFile(cfgPath, initial, 0600); err != nil {
					t.Fatal(err)
				}
				_, stderr, err := runCLI(t, dir, args...)
				if _, ok := err.(*exec.ExitError); !ok || stderr == "" {
					t.Fatalf("args=%v expected failure, got err=%v stderr=%q", args, err, stderr)
				}
				got, readErr := os.ReadFile(cfgPath)
				if readErr != nil || !bytes.Equal(got, initial) {
					t.Fatalf("args=%v config modified on failure: %v %s", args, readErr, got)
				}
			}
		})

		t.Run("success persists token", func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "pcs_config.json")
			initial := []byte(`{"appid":1,"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"name":"alice","workdir":"/"}]}`)
			if err := os.WriteFile(cfgPath, initial, 0600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, err := runCLI(t, dir, "setastoken", "tok-new-123")
			if err != nil || stderr != "" || !strings.Contains(stdout, "tok-new-123") {
				t.Fatalf("expected success, got err=%v stdout=%q stderr=%q", err, stdout, stderr)
			}
			got, readErr := os.ReadFile(cfgPath)
			if readErr != nil || !strings.Contains(string(got), "tok-new-123") {
				t.Fatalf("token not persisted: %v %s", readErr, got)
			}
		})
	})

	t.Run("match", func(t *testing.T) {
		t.Run("bad arg count", func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), []byte(`{"appid":1}`), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"match"}, {"match", "/a", "/b"}} {
				_, stderr, err := runCLI(t, dir, args...)
				if _, ok := err.(*exec.ExitError); !ok || stderr == "" {
					t.Fatalf("args=%v expected failure, got err=%v stderr=%q", args, err, stderr)
				}
			}
		})

		t.Run("remote error propagates", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))
			defer server.Close()
			dir := t.TempDir()
			data, err := json.Marshal(map[string]interface{}{
				"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL,
				"baidu_active_uid": 7, "baidu_user_list": []map[string]interface{}{{"uid": 7, "workdir": "/"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			_, stderr, err := runCLI(t, dir, "match", "/fixture/*")
			if _, ok := err.(*exec.ExitError); !ok || stderr == "" || strings.Contains(stderr, "panic:") {
				t.Fatalf("expected failure, got err=%v stderr=%q", err, stderr)
			}
		})
	})

	t.Run("tool getip error propagates", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()
		dir := t.TempDir()
		data, err := json.Marshal(map[string]interface{}{
			"appid": 1, "enable_https": false, "proxy": server.URL,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		_, stderr, err := runCLI(t, dir, "tool", "getip")
		if _, ok := err.(*exec.ExitError); !ok || !strings.Contains(stderr, "获取公网IP错误") {
			t.Fatalf("expected getip failure, got err=%v stderr=%q", err, stderr)
		}
	})
}
