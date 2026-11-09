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

func TestLegacySearchAndTreeExitCodes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "search-tree.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"search", "tree"} {
		states := []string{"success", "remote", "http", "malformed", "glob-empty"}
		if op == "tree" {
			states = append(states, "children-fail", "depth-limit")
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method == http.MethodConnect {
						http.Error(w, "no external tunnel", 403)
						return
					}
					if r.Method != http.MethodGet {
						t.Errorf("unexpected HTTP method: %s", r.Method)
					}
					if state == "glob-empty" {
						if r.URL.Path != "/api/list" || r.URL.Query().Get("dir") != "/fixture" {
							t.Errorf("unexpected glob request: %s", r.URL)
						}
						w.Write([]byte(`{"errno":0,"list":[]}`))
						return
					}
					if op == "search" {
						q := r.URL.Query()
						if q.Get("method") != "search" || q.Get("path") != "/fixture" || q.Get("wd") != "needle" || q.Get("re") != "1" {
							t.Errorf("wrong search parameters: %s", r.URL)
						}
					} else if r.URL.Path != "/api/list" {
						t.Errorf("unexpected tree request: %s", r.URL)
					}
					if state == "http" {
						http.Error(w, "fixture unavailable", 503)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					if state == "remote" {
						w.Write([]byte(`{"error_code":31045,"errno":31045}`))
						return
					}
					if state == "malformed" {
						w.Write([]byte(`{`))
						return
					}
					if state == "children-fail" || state == "depth-limit" {
						switch r.URL.Query().Get("dir") {
						case "/fixture":
							w.Write([]byte(`{"errno":0,"list":[{"fs_id":1,"path":"/fixture/bad-one","server_filename":"bad-one","isdir":1},{"fs_id":2,"path":"/fixture/bad-two","server_filename":"bad-two","isdir":1},{"fs_id":3,"path":"/fixture/good","server_filename":"good","isdir":1}]}`))
						case "/fixture/bad-one", "/fixture/bad-two":
							w.Write([]byte(`{"errno":31045}`))
						case "/fixture/good":
							w.Write([]byte(`{"errno":0,"list":[{"fs_id":4,"path":"/fixture/good/later-file","server_filename":"later-file","isdir":0,"size":4}]}`))
						default:
							t.Errorf("unexpected descendant: %s", r.URL)
							http.Error(w, "bad dir", 400)
						}
						return
					}
					w.Write([]byte(`{"error_code":0,"errno":0,"list":[{"fs_id":101,"path":"/fixture/fixture-result","server_filename":"fixture-result","isdir":0,"size":4}]}`))
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
				target := "/fixture"
				if state == "glob-empty" {
					target = "/fixture/*"
				}
				args := []string{"search", "-r", "-l", "-path", target, "needle"}
				if op == "tree" {
					args = []string{"tree", "--fsid", target}
				}
				if state == "depth-limit" {
					args = []string{"tree", "--depth", "0", target}
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
				wantCalls := int32(1)
				if state == "children-fail" {
					wantCalls = 4
				}
				if calls.Load() != wantCalls {
					t.Errorf("calls=%d want=%d", calls.Load(), wantCalls)
				}
				if state == "success" || state == "depth-limit" {
					marker := "fixture-result"
					if state == "depth-limit" {
						marker = "good"
					}
					if err != nil || !strings.Contains(stdout.String(), marker) {
						t.Errorf("valid command failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || strings.Contains(stderr.String(), "panic:") {
						t.Errorf("failure returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if state == "children-fail" {
						if !strings.Contains(stdout.String(), "later-file") || !strings.Contains(stderr.String(), "/fixture/bad-one") || !strings.Contains(stderr.String(), "/fixture/bad-two") {
							t.Errorf("partial result or error paths lost: stdout=%s stderr=%s", stdout.String(), stderr.String())
						}
					} else if strings.Contains(stdout.String(), "fixture-result") {
						t.Errorf("failed command printed data: %s", stdout.String())
					}
				}
			})
		}
	}
}
