//go:build unix

package pcscommand

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

func TestUploadActiveSignals(t *testing.T) {
	const modeEnv = "PCS_FIXTURE_ACTIVE_UPLOAD_SIGNAL"
	const rootEnv = "PCS_FIXTURE_ACTIVE_UPLOAD_ROOT"
	if mode := os.Getenv(modeEnv); mode != "" {
		root := os.Getenv(rootEnv)
		if !filepath.IsAbs(root) {
			t.Fatal("missing isolated fixture root")
		}
		pcsconfig.Config = pcsconfig.NewConfig(filepath.Join(root, "synthetic.json"))
		pcsconfig.Config.InitDefaultConfig()
		t.Setenv(pcsconfig.EnvConfigDir, root)
		var paths []string
		for _, name := range []string{"first.txt", "second.txt"} {
			p := filepath.Join(root, name)
			if err := os.WriteFile(p, []byte("abcd"), 0600); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, p)
		}
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		var first os.Signal = syscall.SIGTERM
		if strings.HasPrefix(mode, "interrupt/") {
			first = os.Interrupt
		}
		var executor *taskframework.TaskExecutor
		requests := 0
		GetBaiduPCS().GetClient().Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
			requests++
			if req.Body != nil {
				defer req.Body.Close()
			}
			if req.URL.Path != "/api/list" || req.URL.Query().Get("dir") != "/fixture" {
				t.Fatalf("unexpected request: %s", req.URL)
			}
			if err := self.Signal(first); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for !executor.Stopped() {
				if time.Now().After(deadline) {
					t.Fatal("first signal did not stop dispatch")
				}
				time.Sleep(time.Millisecond)
			}
			if strings.HasSuffix(mode, "/force") {
				if err := self.Signal(first); err != nil {
					t.Fatal(err)
				}
				// Keep the first task active. Only the production signal handler
				// may end this child; the parent's deadline bounds a broken handler.
				select {}
			}
			payload := `{"errno":0,"list":[{"fs_id":1,"path":"/fixture/first.txt","server_filename":"first.txt","size":4,"md5":"` + exportFixtureMD5 + `"}]}`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
		})
		err = runUpload(paths, "/fixture", &UploadOptions{Load: 1, Policy: baidupcs.SkipPolicy}, func(e *taskframework.TaskExecutor) func() { executor = e; return watchUploadSignals(e) })
		if !errors.Is(err, context.Canceled) || executor.Count() != 1 || requests != 1 {
			t.Fatalf("signal cancellation lost: err=%v pending=%d requests=%d", err, executor.Count(), requests)
		}
		if n := uploadSignalWaiters(); n != 0 {
			t.Fatalf("signal handler remained after cancel: %d", n)
		}
		for _, p := range paths {
			if data, err := os.ReadFile(p); err != nil || string(data) != "abcd" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		}
		if strings.HasSuffix(mode, "/restore") {
			if err := self.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			time.Sleep(250 * time.Millisecond)
			t.Fatal("upload listener consumed a signal after cancellation returned")
		}
		return
	}
	for _, first := range []string{"interrupt", "terminate"} {
		for _, outcome := range []string{"return", "restore", "force"} {
			t.Run(first+"/"+outcome, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadActiveSignals$")
				cmd.Env = append(os.Environ(), modeEnv+"="+first+"/"+outcome, rootEnv+"="+t.TempDir())
				out, err := cmd.CombinedOutput()
				if ctx.Err() != nil {
					t.Fatalf("signal child timed out: %v output=%s", ctx.Err(), out)
				}
				switch outcome {
				case "return":
					if err != nil || !strings.Contains(string(out), "上传已取消, 剩余文件数: 1") || strings.Contains(string(out), "上传结束") {
						t.Fatalf("canceled child did not return cleanly: %v output=%s", err, out)
					}
				case "restore":
					exit, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatalf("default signal disposition not restored: %v output=%s", err, out)
					}
					status, ok := exit.Sys().(syscall.WaitStatus)
					if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
						t.Fatalf("unexpected exit after cancellation: %v output=%s", err, out)
					}
				case "force":
					exit, ok := err.(*exec.ExitError)
					if !ok || exit.ExitCode() != 130 || !strings.Contains(string(out), "强制退出") {
						t.Fatalf("second signal did not force exit 130: %v output=%s", err, out)
					}
				default:
					t.Fatalf("unexpected outcome %q", outcome)
				}
			})
		}
	}
}
