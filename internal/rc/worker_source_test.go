package rc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestUploadWorkerValidatesSourceBeforeRequests(t *testing.T) {
	for _, kind := range []string{"directory", "missing", "empty-file", "empty-hardlink", "file-symlink", "directory-symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "source")
			switch kind {
			case "directory":
				if err := os.Mkdir(source, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(source, "keep"), []byte("precious source"), 0600); err != nil {
					t.Fatal(err)
				}
			case "empty-file":
				if err := os.WriteFile(source, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "empty-hardlink":
				target := filepath.Join(root, "target")
				if err := os.WriteFile(target, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(target, source); err != nil {
					t.Fatal(err)
				}
			case "file-symlink", "directory-symlink":
				target := filepath.Join(root, "target")
				var err error
				if kind == "file-symlink" {
					err = os.WriteFile(target, nil, 0600)
				} else {
					err = os.Mkdir(target, 0700)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, source); err != nil {
					t.Skipf("cannot create symlink fixture: %v", err)
				}
			}
			valid := kind == "empty-file" || kind == "empty-hardlink"
			checkUploadSourceWorker(t, source, valid)
			if valid {
				if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
					t.Fatalf("empty source changed: %v %v", info, err)
				}
			}
			if kind == "directory" {
				if data, err := os.ReadFile(filepath.Join(source, "keep")); err != nil || string(data) != "precious source" {
					t.Fatalf("source directory contents changed: %q %v", data, err)
				}
			}
		})
	}
}

func checkUploadSourceWorker(t *testing.T, source string, valid bool) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, `{"error_code":0}`)
	}))
	defer server.Close()
	configRoot := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadSourceWorkerProcessFixture$")
	cmd.Env = append(os.Environ(), "PCS_SOURCE_WORKER_PATH="+source, "PCS_SOURCE_WORKER_PROXY="+server.URL, "PCS_SOURCE_WORKER_CONFIG="+configRoot)
	if valid {
		cmd.Env = append(cmd.Env, "PCS_SOURCE_WORKER_VALID=1")
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("source validation worker failed: %v %s", err, output)
	}
	wantCalls := int32(0)
	if valid {
		wantCalls = 1
	}
	if calls.Load() != wantCalls {
		t.Errorf("invalid source reached remote API: calls=%d want=%d", calls.Load(), wantCalls)
	}
	if _, err := os.Stat(filepath.Join(configRoot, "rc-state")); !os.IsNotExist(err) {
		t.Errorf("source validation created upload resume state: %v", err)
	}
}

func TestUploadSourceWorkerProcessFixture(t *testing.T) {
	source := os.Getenv("PCS_SOURCE_WORKER_PATH")
	if source == "" {
		return
	}
	root := os.Getenv("PCS_SOURCE_WORKER_CONFIG")
	pcsconfig.SetConfigDir(root)
	cfg := pcsconfig.NewConfig(filepath.Join(root, "config.json"))
	cfg.AppID, cfg.PCSAddr, cfg.EnableHTTPS = 1, "pcs.baidu.com", false
	cfg.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
	pcsconfig.Config = cfg
	requester.SetGlobalProxy(os.Getenv("PCS_SOURCE_WORKER_PROXY"))
	requester.SetProxyHostnameRules("")
	err := RunWorker([]string{"upload", "7", source, "/target", "0"})
	if os.Getenv("PCS_SOURCE_WORKER_VALID") == "1" {
		if err != nil {
			t.Fatal("valid empty source rejected", err)
		}
	} else if err == nil {
		t.Fatal("unsupported source accepted for upload")
	}
}
