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

func TestLegacyOfflineValidationAndExitCodes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "offline.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"add", "query", "list", "cancel", "delete", "clear"} {
		states := []string{"success", "remote", "http", "malformed"}
		if op == "query" || op == "cancel" || op == "delete" {
			states = append(states, "mixed", "invalid", "zero", "overflow")
		}
		if op == "clear" {
			states = append(states, "conflict")
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method == http.MethodConnect {
						http.Error(w, "no external tunnel", 403)
						return
					}
					method := op + "_task"
					if r.URL.Query().Get("method") != method || !strings.HasSuffix(r.URL.Path, "/services/cloud_dl") {
						t.Errorf("unexpected offline request: %s %s", r.Method, r.URL)
						http.Error(w, "bad request", 400)
						return
					}
					wantMethod := http.MethodPost
					if op == "query" {
						wantMethod = http.MethodGet
					}
					if r.Method != wantMethod {
						t.Errorf("HTTP method=%s want=%s", r.Method, wantMethod)
					}
					if op == "query" && r.URL.Query().Get("task_ids") != "101,202" {
						t.Errorf("wrong query IDs: %s", r.URL)
					}
					if op == "cancel" || op == "delete" {
						if id := r.URL.Query().Get("task_id"); id != "101" && id != "202" {
							t.Errorf("wrong task ID: %q", id)
						}
					}
					if op == "add" && (r.URL.Query().Get("source_url") != "https://fixture.invalid/file" || r.URL.Query().Get("save_path") != "/fixture/") {
						t.Errorf("wrong add parameters: %s", r.URL)
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
					if state == "remote" {
						w.Write([]byte(`{"error_code":31045}`))
						return
					}
					switch op {
					case "add":
						w.Write([]byte(`{"error_code":0,"task_id":101}`))
					case "query":
						w.Write([]byte(`{"error_code":0,"task_info":{"101":{"task_name":"fixture-one"},"202":{"task_name":"fixture-two"}}}`))
					case "list":
						w.Write([]byte(`{"error_code":0,"task_info":[{"task_id":"101","task_name":"fixture-one"}]}`))
					case "clear":
						w.Write([]byte(`{"error_code":0,"total":2}`))
					default:
						w.Write([]byte(`{"error_code":0}`))
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
				args := []string{"offlinedl", op}
				switch op {
				case "add":
					args = append(args, "-path", "/fixture", "https://fixture.invalid/file")
				case "query", "cancel", "delete":
					ids := []string{"101", "202"}
					switch state {
					case "mixed":
						ids[1] = "invalid"
					case "invalid":
						ids = []string{"invalid"}
					case "zero":
						ids[1] = "0"
					case "overflow":
						ids[1] = "9223372036854775808"
					}
					args = append(args, ids...)
				case "clear":
					args = []string{"offlinedl", "delete", "-all"}
					if state == "conflict" {
						args = append(args, "101")
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
				wantRequests := int32(1)
				if op == "cancel" || op == "delete" {
					wantRequests = 2
				}
				if state == "mixed" || state == "invalid" || state == "zero" || state == "overflow" || state == "conflict" {
					wantRequests = 0
				}
				if requests.Load() != wantRequests {
					t.Errorf("requests=%d want=%d", requests.Load(), wantRequests)
				}
				if state == "success" {
					if err != nil || stdout.Len() == 0 {
						t.Errorf("success failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || strings.Contains(stderr.String(), "panic:") {
						t.Errorf("failure returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if strings.Contains(stdout.String(), "成功") {
						t.Errorf("failed command reported success: %s", stdout.String())
					}
				}
			})
		}
	}
}
