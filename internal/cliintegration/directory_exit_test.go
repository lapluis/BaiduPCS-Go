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

func TestLegacyDirectoryExitAndState(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "directory-fixture.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, state := range []string{"metadata-error", "not-directory", "success", "ls-error"} {
		t.Run(state, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if state == "ls-error" {
					// Refuse CONNECT locally; never tunnel to a real Pan host.
					if r.Method == http.MethodConnect {
						http.Error(w, "fixture proxy refused", http.StatusForbidden)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, `{"errno":2}`)
					}
					return
				}
				if r.Method != http.MethodPost || r.URL.Query().Get("method") != "meta" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					http.Error(w, "bad method", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if state == "metadata-error" {
					fmt.Fprint(w, `{"error_code":31045}`)
					return
				}
				dir := 1
				if state == "not-directory" {
					dir = 0
				}
				fmt.Fprintf(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/target","isdir":%d}]}`, dir)
			}))
			defer server.Close()
			dir := t.TempDir()
			config := map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]interface{}{{"uid": 7, "name": "fixture", "workdir": "/old"}}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(dir, "pcs_config.json")
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			op := "cd"
			if state == "ls-error" {
				op = "ls"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, op, "/target")
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
			wantDirectory := "/old"
			if state == "success" {
				wantDirectory = "/target"
				if err != nil || !bytes.Contains(stdout.Bytes(), []byte("改变工作目录")) {
					t.Errorf("valid cd failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
			} else {
				if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
					t.Errorf("failed directory command returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
				if bytes.Contains(stdout.Bytes(), []byte("改变工作目录")) {
					t.Errorf("failed cd reported success: %s", stdout.String())
				}
			}
			if requests.Load() != 1 {
				t.Errorf("requests=%d want 1", requests.Load())
			}
			data, err = os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Users []struct {
					Workdir string `json:"workdir"`
				} `json:"baidu_user_list"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if len(saved.Users) != 1 || saved.Users[0].Workdir != wantDirectory {
				t.Errorf("saved directory changed incorrectly: %s", data)
			}
		})
	}
}
