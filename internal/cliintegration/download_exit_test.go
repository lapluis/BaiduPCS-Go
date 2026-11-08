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
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyDownloadExitStatus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "download-fixture.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, kind := range []string{"invalid-mode", "metadata-error", "output-error", "success"} {
		t.Run(kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Query().Get("method") != "meta" {
					t.Errorf("unexpected fixture request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if kind == "metadata-error" {
					fmt.Fprint(w, `{"error_code":31066,"error_msg":"fixture missing"}`)
					return
				}
				fmt.Fprint(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/fixture.txt","size":0}]}`)
			}))
			defer server.Close()
			dir := t.TempDir()
			config := map[string]interface{}{"appid": 1, "pcs_addr": "pcs.baidu.com", "enable_https": false, "cache_size": 8192, "proxy": server.URL, "baidu_active_uid": 7, "baidu_user_list": []map[string]int{{"uid": 7}}}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(dir, "target")
			blocker := filepath.Join(dir, "retained")
			if kind == "output-error" {
				if err := os.WriteFile(blocker, []byte("sole-copy"), 0600); err != nil {
					t.Fatal(err)
				}
				target = filepath.Join(blocker, "child")
			}
			mode := "pcs"
			if kind == "invalid-mode" {
				mode = "invalid-fixture"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "download", "-mode", mode, "-retry", "0", "-saveto", target, "/fixture.txt")
			cmd.Dir = dir
			for _, entry := range os.Environ() {
				if !bytes.HasPrefix(bytes.ToUpper([]byte(entry)), []byte("BAIDUPCS_GO_CONFIG_DIR=")) {
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
			if kind == "success" {
				if err != nil {
					t.Fatalf("valid empty download failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
				info, err := os.Stat(filepath.Join(target, "fixture.txt"))
				if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
					t.Fatalf("empty output missing: %v", err)
				}
			} else {
				if err == nil || stderr.Len() == 0 {
					t.Errorf("failed download returned success or lost stderr: err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
				if bytes.Contains(stdout.Bytes(), []byte("下载结束")) {
					t.Error("failed download reported normal completion")
				}
			}
			wantRequests := int32(1)
			if kind == "invalid-mode" {
				wantRequests = 0
			}
			if requests.Load() != wantRequests {
				t.Errorf("requests=%d want %d", requests.Load(), wantRequests)
			}
			if kind == "output-error" {
				if data, err := os.ReadFile(blocker); err != nil || string(data) != "sole-copy" {
					t.Fatalf("existing file changed: %q %v", data, err)
				}
			}
		})
	}
}
