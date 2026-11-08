package pcscommand

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func changeDirectoryFixture(t *testing.T) string {
	t.Helper()
	previous := pcsconfig.Config
	name := filepath.Join(t.TempDir(), "synthetic.json")
	pcsconfig.Config = pcsconfig.NewConfig(name)
	pcsconfig.Config.InitDefaultConfig()
	user := &pcsconfig.Baidu{BaiduBase: pcsconfig.BaiduBase{UID: 7, Name: "fixture"}, Workdir: "/old"}
	pcsconfig.Config.BaiduUserList = pcsconfig.BaiduUserList{user}
	if _, err := pcsconfig.Config.SwitchUser(&user.BaiduBase); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pcsconfig.Config = previous })
	if err := pcsconfig.Config.Save(); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestChangeDirectorySaveFailurePreservesWorkingDirectory(t *testing.T) {
	for _, list := range []bool{false, true} {
		t.Run(fmt.Sprintf("list=%t", list), func(t *testing.T) {
			name := changeDirectoryFixture(t)
			original, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			retained := name + ".retained"
			if err := os.Rename(name, retained); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(name, 0700); err != nil {
				t.Fatal(err)
			}
			requests := 0
			GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"error_code":0,"list":[{"fs_id":1,"path":"/target","isdir":1}]}`
				if req.URL.Path == "/api/list" {
					payload = `{"errno":0,"list":[]}`
				} else if req.URL.Query().Get("method") != "meta" {
					t.Fatalf("unexpected request: %s", req.URL)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			log := captureExportCall(t, func() { RunChangeDirectory("/target", list) })
			if GetActiveUser().Workdir != "/old" || strings.Contains(log, "改变工作目录") || requests != 1 {
				t.Errorf("failed save published directory: workdir=%q requests=%d log=%q", GetActiveUser().Workdir, requests, log)
			}
			if data, err := os.ReadFile(retained); err != nil || string(data) != string(original) {
				t.Errorf("retained config changed: %v", err)
			}
			if info, err := os.Stat(name); err != nil || !info.IsDir() {
				t.Errorf("blocking directory changed: %v", err)
			}
		})
	}
}
