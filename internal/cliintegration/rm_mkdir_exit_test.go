package cliintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func TestLegacyRemoveMkdirExitStatus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "mutation-fixture.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"rm", "mkdir"} {
		for _, state := range []string{"remote-error", "http-error", "malformed", "success"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					wantMethod := "delete"
					if op == "mkdir" {
						wantMethod = "mkdir"
					}
					if r.Method != http.MethodPost || r.URL.Query().Get("method") != wantMethod {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.Error(w, "bad method", 400)
						return
					}
					if op == "mkdir" {
						if r.URL.Query().Get("path") != "/fixture" {
							t.Errorf("wrong mkdir path: %s", r.URL)
						}
					} else {
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
						if err := json.Unmarshal([]byte(r.FormValue("param")), &params); err != nil {
							t.Error(err)
							http.Error(w, "bad params", 400)
							return
						}
						if len(params.List) != 2 || params.List[0].Path != "/one" || params.List[1].Path != "/two" {
							t.Errorf("wrong deletion paths: %+v", params)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					switch state {
					case "remote-error":
						fmt.Fprint(w, `{"error_code":31045}`)
					case "http-error":
						w.WriteHeader(http.StatusServiceUnavailable)
						fmt.Fprint(w, `{"error_code":0}`)
					case "malformed":
						fmt.Fprint(w, `{`)
					case "success":
						fmt.Fprint(w, `{"error_code":0}`)
					}
				}))
				defer server.Close()
				dir := t.TempDir()
				config := map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]int{{"uid": 7}}}
				data, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{op, "/one", "/two"}
				if op == "mkdir" {
					args = []string{op, "/fixture"}
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
					t.Fatalf("fixture timed out: %v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
				}
				if state == "success" {
					if err != nil || !bytes.Contains(stdout.Bytes(), []byte("成功")) {
						t.Errorf("valid operation failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
						t.Errorf("failed operation returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if bytes.Contains(stdout.Bytes(), []byte("成功")) {
						t.Errorf("failed operation reported success: %s", stdout.String())
					}
				}
				if requests.Load() != 1 {
					t.Errorf("requests=%d want 1", requests.Load())
				}
			})
		}
	}
}
