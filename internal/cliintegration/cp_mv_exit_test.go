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

func TestLegacyCopyMoveExitAndPreflight(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "remote-operations.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, op := range []string{"cp", "mv"} {
		for _, state := range []string{"inspection-error", "operation-error", "directory", "missing"} {
			t.Run(op+"/"+state, func(t *testing.T) {
				var requests, mutations atomic.Int32
				expectedMethod := "copy"
				if op == "mv" {
					expectedMethod = "move"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					method := r.URL.Query().Get("method")
					if r.Method != http.MethodPost {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL)
						http.Error(w, "bad method", 400)
						return
					}
					switch method {
					case "meta":
						if state == "inspection-error" {
							fmt.Fprint(w, `{"error_code":31045}`)
						} else if state == "missing" {
							fmt.Fprint(w, `{"error_code":31066}`)
						} else {
							fmt.Fprint(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/target","isdir":1}]}`)
						}
					case expectedMethod:
						mutations.Add(1)
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
								From string `json:"from"`
								To   string `json:"to"`
							} `json:"list"`
						}
						if err := json.Unmarshal([]byte(r.FormValue("param")), &params); err != nil {
							t.Error(err)
							http.Error(w, "bad params", 400)
							return
						}
						wantTarget := "/target/source"
						if state == "missing" || state == "inspection-error" {
							wantTarget = "/target"
						}
						if len(params.List) != 1 || params.List[0].From != "/source" || params.List[0].To != wantTarget {
							t.Errorf("unexpected wire paths: %+v", params)
						}
						if state == "operation-error" {
							fmt.Fprint(w, `{"error_code":31061}`)
						} else {
							fmt.Fprint(w, `{"error_code":0}`)
						}
					default:
						t.Errorf("unexpected fixture request: %s", r.URL)
						http.Error(w, "unexpected request", 400)
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
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, op, "/source", "/target")
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
				failure := state == "inspection-error" || state == "operation-error"
				if failure {
					if _, ok := err.(*exec.ExitError); !ok || stderr.Len() == 0 || bytes.Contains(stderr.Bytes(), []byte("panic:")) {
						t.Errorf("remote failure not reported: err=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
					}
					if bytes.Contains(stdout.Bytes(), []byte("成功")) {
						t.Errorf("failed operation reported success: %s", stdout.String())
					}
				} else if err != nil || !bytes.Contains(stdout.Bytes(), []byte("成功")) {
					t.Errorf("valid operation failed: %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
				}
				wantRequests, wantMutations := int32(2), int32(1)
				if state == "inspection-error" {
					wantRequests, wantMutations = 1, 0
				}
				if requests.Load() != wantRequests || mutations.Load() != wantMutations {
					t.Errorf("requests=%d mutations=%d want %d/%d", requests.Load(), mutations.Load(), wantRequests, wantMutations)
				}
			})
		}
	}
}
