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

func TestLegacyQuotaAndMetaExitCodes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "quota-meta.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	const md5 = "e2fc714c4727ee9395f324cd2e7f331f"
	for _, op := range []string{"quota", "meta"} {
		states := []string{"success", "remote", "http", "malformed"}
		if op == "quota" {
			states = append(states, "zero-empty", "zero-used")
		} else {
			states = append(states, "glob-empty", "partial")
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
					if state == "glob-empty" {
						if r.URL.Path != "/api/list" || r.URL.Query().Get("dir") != "/fixture" {
							t.Errorf("unexpected glob request: %s", r.URL)
						}
						w.Write([]byte(`{"errno":0,"list":[]}`))
						return
					}
					method := "info"
					if op == "meta" {
						method = "meta"
					}
					if r.URL.Query().Get("method") != method {
						t.Errorf("unexpected method: %s", r.URL)
						http.Error(w, "bad request", 400)
						return
					}
					path := ""
					if op == "meta" {
						if err := r.ParseMultipartForm(4096); err != nil {
							t.Error(err)
							http.Error(w, "bad form", 400)
							return
						}
						if r.MultipartForm != nil {
							defer r.MultipartForm.RemoveAll()
						}
						var params struct {
							List []struct {
								Path string `json:"path"`
							} `json:"list"`
						}
						if err := json.Unmarshal([]byte(r.FormValue("param")), &params); err != nil || len(params.List) != 1 {
							t.Errorf("bad metadata params: %v %+v", err, params)
							http.Error(w, "bad params", 400)
							return
						}
						path = params.List[0].Path
						if path != "/fixture-file" && path != "/first-file" && path != "/bad-file" {
							t.Errorf("unexpected metadata path: %s", path)
						}
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
					if state == "remote" || path == "/bad-file" {
						w.Write([]byte(`{"error_code":31045}`))
						return
					}
					if op == "quota" {
						total, used := 8, 4
						if strings.HasPrefix(state, "zero") {
							total = 0
							used = 0
							if state == "zero-used" {
								used = 5
							}
						}
						json.NewEncoder(w).Encode(map[string]int{"error_code": 0, "quota": total, "used": used})
					} else {
						json.NewEncoder(w).Encode(map[string]interface{}{"error_code": 0, "list": []map[string]interface{}{{"fs_id": 101, "path": path, "size": 4, "md5": md5, "block_list": []string{md5}}}})
					}
				}))
				defer server.Close()
				dir := t.TempDir()
				data, err := json.Marshal(map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]interface{}{{"uid": 7, "name": "fixture"}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{op}
				if op == "meta" {
					args = append(args, "/fixture-file")
				}
				if state == "glob-empty" {
					args = []string{"meta", "/fixture/*"}
				}
				if state == "partial" {
					args = []string{"meta", "/first-file", "/bad-file", "/never-file"}
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
				if state == "partial" {
					wantCalls = 2
				}
				if calls.Load() != wantCalls {
					t.Errorf("calls=%d want=%d", calls.Load(), wantCalls)
				}
				if state == "success" || strings.HasPrefix(state, "zero") {
					if err != nil || stdout.Len() == 0 {
						t.Errorf("valid command failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if op == "meta" && !strings.Contains(stdout.String(), md5) {
						t.Errorf("metadata missing: %s", stdout.String())
					}
					if op == "quota" && state == "success" && !strings.Contains(stdout.String(), "50.000000%") {
						t.Errorf("ratio changed: %s", stdout.String())
					}
					if strings.HasPrefix(state, "zero") && (strings.Contains(stdout.String(), "NaN") || strings.Contains(stdout.String(), "Inf") || !strings.Contains(stdout.String(), "--")) {
						t.Errorf("zero capacity produced invalid ratio: %s", stdout.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || strings.Contains(stderr.String(), "panic:") {
						t.Errorf("failure returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if state == "partial" {
						if !strings.Contains(stdout.String(), md5) || !strings.Contains(stderr.String(), "/bad-file") {
							t.Errorf("partial result or failed path lost: stdout=%s stderr=%s", stdout.String(), stderr.String())
						}
					} else if strings.Contains(stdout.String(), md5) {
						t.Errorf("failed command printed metadata: %s", stdout.String())
					}
				}
			})
		}
	}
}
