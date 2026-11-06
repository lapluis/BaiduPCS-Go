package rc

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func TestEmptyWorkerRespectsDownloadOwnership(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"error_code":0,"list":[{"fs_id":1,"path":"/empty","isdir":0,"size":0}]}`)
	}))
	defer server.Close()
	root := t.TempDir()
	path := filepath.Join(root, "data")
	if err := os.WriteFile(path, []byte("only copy"), 0600); err != nil {
		t.Fatal(err)
	}
	checkpoint := path + pcsdownload.DownloadSuffix
	if err := os.WriteFile(checkpoint, []byte("active progress"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(checkpoint, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	owner := downloader.NewInstanceState(file, downloader.InstanceStateStorageFormatJSON)
	defer owner.Close()
	for _, phase := range []string{"busy", "free"} {
		if phase == "free" {
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEmptyWorkerProcessFixture$")
		cmd.Env = append(os.Environ(), "PCS_EMPTY_WORKER_OUTPUT="+path, "PCS_EMPTY_WORKER_PROXY="+server.URL, "PCS_EMPTY_WORKER_PHASE="+phase)
		input, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		output, err := cmd.CombinedOutput()
		input.Close()
		cancel()
		if err != nil {
			t.Errorf("child %s failed: %v %s", phase, err, output)
		}
		if phase == "busy" {
			if got, err := os.ReadFile(path); err != nil || string(got) != "only copy" {
				t.Errorf("worker changed active output: %q %v", got, err)
			}
			if got, err := os.ReadFile(checkpoint); err != nil || string(got) != "active progress" {
				t.Errorf("worker changed active ranges: %q %v", got, err)
			}
		} else {
			if got, err := os.ReadFile(path); err != nil || len(got) != 0 {
				t.Errorf("worker failed after release: %q %v", got, err)
			}
			if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
				t.Errorf("worker retained stale ranges: %v", err)
			}
		}
	}
}

func TestEmptyWorkerProcessFixture(t *testing.T) {
	path := os.Getenv("PCS_EMPTY_WORKER_OUTPUT")
	if path == "" {
		return
	}
	cfg := pcsconfig.NewConfig(filepath.Join(t.TempDir(), "config.json"))
	cfg.AppID = 1
	cfg.EnableHTTPS = false
	cfg.PCSAddr = "pcs.baidu.com"
	cfg.BaiduUserList = pcsconfig.BaiduUserList{&pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7}}}
	pcsconfig.Config = cfg
	requester.SetGlobalProxy(os.Getenv("PCS_EMPTY_WORKER_PROXY"))
	requester.SetProxyHostnameRules("")
	err := RunWorker([]string{"download", "7", "/empty", path, "0"})
	if os.Getenv("PCS_EMPTY_WORKER_PHASE") == "busy" {
		if err == nil {
			t.Fatal("empty worker bypassed active checkpoint")
		}
	} else if err != nil {
		t.Fatal(err)
	}
}
