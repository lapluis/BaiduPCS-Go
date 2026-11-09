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

func TestLegacyShareAndRecycleListExitCodes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "list-commands.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"share", "recycle"} {
		states := []string{"success", "remote", "http", "malformed"}
		if op == "share" {
			states = []string{"proxy"}
		} // The legacy endpoint requires HTTPS; never forward its CONNECT.
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				var calls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					if r.Method == http.MethodConnect {
						http.Error(w, "fixture refuses external tunnel", 403)
						return
					}
					if op != "recycle" || r.Method != http.MethodGet || r.URL.Path != "/api/recycle/list" || r.URL.Query().Get("page") != "2" {
						t.Errorf("unexpected list request: %s %s", r.Method, r.URL)
						http.Error(w, "bad request", 400)
						return
					}
					if state == "http" {
						http.Error(w, "fixture unavailable", 503)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					switch state {
					case "remote":
						w.Write([]byte(`{"errno":31045}`))
					case "malformed":
						w.Write([]byte(`{`))
					default:
						w.Write([]byte(`{"errno":0,"list":[{"fs_id":101,"path":"/fixture-file","isdir":0,"size":4}]}`))
					}
				}))
				defer server.Close()
				dir := t.TempDir()
				data, err := json.Marshal(map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]int{{"uid": 7}}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, op, "list", "-page", "2")
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
				if calls.Load() != 1 {
					t.Errorf("calls=%d want=1", calls.Load())
				}
				if state == "success" {
					if err != nil || !strings.Contains(stdout.String(), "/fixture-file") {
						t.Errorf("valid list failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || strings.Contains(stderr.String(), "panic:") {
						t.Errorf("failed list returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if strings.Contains(stdout.String(), "/fixture-file") {
						t.Errorf("failed list printed data: %s", stdout.String())
					}
				}
			})
		}
	}
}
