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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLegacyIDMutationsValidateWholeInput(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "id-operations.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"share", "delete", "restore", "clear"} {
		states := []string{"mixed-invalid", "remote-error", "success"}
		if op == "share" {
			// The actual CLI uses mandatory HTTPS here. Refuse its tunnel;
			// successful API responses are covered by the in-memory unit fixture.
			states = states[:2]
		}
		for _, state := range states {
			t.Run(op+"/"+state, func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method == http.MethodConnect {
						http.Error(w, "fixture refuses external tunnel", http.StatusForbidden)
						return
					}
					var ids []int64
					if op == "clear" {
						if r.Method != http.MethodGet || r.URL.Query().Get("method") != "delete" || r.URL.Query().Get("type") != "recycle" {
							t.Errorf("unexpected clear request: %s %s", r.Method, r.URL)
							http.Error(w, "bad request", 400)
							return
						}
					} else if op == "restore" {
						if r.Method != http.MethodPost || r.URL.Query().Get("method") != "restore" {
							t.Errorf("unexpected restore request: %s %s", r.Method, r.URL)
							http.Error(w, "bad request", 400)
							return
						}
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
								ID int64 `json:"fs_id"`
							} `json:"list"`
						}
						if err := json.Unmarshal([]byte(r.FormValue("param")), &params); err != nil {
							t.Error(err)
							http.Error(w, "bad params", 400)
							return
						}
						for _, item := range params.List {
							ids = append(ids, item.ID)
						}
					} else {
						wantPath, field := "/share/cancel", "shareid_list"
						if op == "delete" {
							wantPath, field = "/api/recycle/delete", "fidlist"
						}
						if r.Method != http.MethodPost || r.URL.Path != wantPath {
							t.Errorf("unexpected mutation request: %s %s", r.Method, r.URL)
							http.Error(w, "bad request", 400)
							return
						}
						if err := r.ParseForm(); err != nil {
							t.Error(err)
							http.Error(w, "bad form", 400)
							return
						}
						if err := json.Unmarshal([]byte(r.FormValue(field)), &ids); err != nil {
							t.Error(err)
							http.Error(w, "bad ids", 400)
							return
						}
					}
					if op != "clear" {
						want := []int64{101, 202}
						if state == "mixed-invalid" {
							want = want[:1]
						}
						if !reflect.DeepEqual(ids, want) {
							t.Errorf("wrong wire IDs: %v want %v", ids, want)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					if state == "remote-error" {
						json.NewEncoder(w).Encode(map[string]int{"error_code": 31045})
						return
					}
					var restored []map[string]int64
					for _, id := range ids {
						restored = append(restored, map[string]int64{"fs_id": id})
					}
					json.NewEncoder(w).Encode(map[string]interface{}{"error_code": 0, "errno": 0, "extra": map[string]interface{}{"list": restored, "succNum": 2}})
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
				args := []string{"recycle", op}
				if op == "share" {
					args = []string{"share", "cancel"}
				}
				if op == "clear" {
					args = []string{"recycle", "delete", "-all"}
					if state == "mixed-invalid" {
						args = append(args, "101")
					}
				} else {
					args = append(args, "101", "202")
					if state == "mixed-invalid" {
						args[len(args)-1] = "invalid-fixture"
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
					t.Fatalf("fixture timed out: %v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
				}
				if state == "success" {
					if err != nil || !bytes.Contains(stdout.Bytes(), []byte("成功")) {
						t.Errorf("valid input failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
				} else {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
						t.Errorf("invalid or rejected operation returned success: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if bytes.Contains(stdout.Bytes(), []byte("成功")) {
						t.Errorf("failed operation reported success: %s", stdout.String())
					}
				}
				wantRequests := int32(1)
				if state == "mixed-invalid" {
					wantRequests = 0
				}
				if requests.Load() != wantRequests {
					t.Errorf("requests=%d want %d", requests.Load(), wantRequests)
				}
			})
		}
	}
}
