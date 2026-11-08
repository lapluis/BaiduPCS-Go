//go:build unix

package pcscommand

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestUploadRestoresSignalHandlingAfterReturn(t *testing.T) {
	const helperEnv = "PCS_FIXTURE_UPLOAD_SIGNAL_HELPER"
	if os.Getenv(helperEnv) == "1" {
		exportFixture(t)
		t.Setenv(pcsconfig.EnvConfigDir, t.TempDir())
		if err := RunUpload([]string{t.TempDir()}, "/fixture", &UploadOptions{}); err == nil {
			t.Fatal("empty upload returned success")
		}
		self, err := os.FindProcess(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		if err := self.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		// A leaked upload listener consumes SIGTERM instead of the process
		// terminating with its normal signal disposition.
		time.Sleep(250 * time.Millisecond)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadRestoresSignalHandlingAfterReturn$")
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	exit, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("SIGTERM was consumed after upload returned: err=%v output=%s", err, out)
	}
	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("unexpected child exit: %v output=%s", err, out)
	}
}
