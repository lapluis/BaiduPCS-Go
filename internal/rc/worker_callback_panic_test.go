package rc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerParentCallbackPanicJoinsChild(t *testing.T) {
	for _, phase := range []string{"typed", "string", "checkpoint", "checkpoint-upload", "canceled", "canceled-checkpoint", "ignores-cancel"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerParentPanicProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_WORKER_PARENT_PANIC="+phase)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("parent callback killed or failed process: %v %s", err, output)
			}
		})
	}
}

func TestWorkerParentPanicProcessFixture(t *testing.T) {
	phase := os.Getenv("PCS_WORKER_PARENT_PANIC")
	if phase == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	checkpoint := filepath.Join(t.TempDir(), "saved")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerParentPanicChildFixture$")
	cmd.Env = append(os.Environ(), "PCS_WORKER_PARENT_PANIC_CHILD="+phase, "PCS_WORKER_PARENT_PANIC_CHECKPOINT="+checkpoint)
	cause := errors.New("synthetic parent progress callback panic")
	calls := 0
	progressCtx := context.WithValue(ctx, progressKey{}, func(transferProgress) {
		calls++
		if strings.HasPrefix(phase, "canceled") {
			cancel()
		}
		if phase == "string" {
			panic(cause.Error())
		}
		panic(cause)
	})
	kind := "download"
	if phase == "string" || phase == "checkpoint-upload" {
		kind = "upload"
	}
	err := executeWorkerCommand(progressCtx, cmd, kind, 7)
	if !errors.Is(err, ErrProgressCallback) || !strings.Contains(err.Error(), cause.Error()) || (phase != "string" && !errors.Is(err, cause)) {
		t.Fatalf("callback panic lost: %v", err)
	}
	if errors.Is(err, context.Canceled) != strings.HasPrefix(phase, "canceled") || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("callback failure classified as cancellation: %v", err)
	}
	checkpointFails := strings.Contains(phase, "checkpoint")
	if errors.Is(err, ErrCheckpointSave) != checkpointFails {
		t.Errorf("post-panic checkpoint report lost: %v", err)
	}
	if calls != 1 {
		t.Errorf("callback invoked again after panic: %d", calls)
	}
	if cmd.ProcessState == nil {
		t.Fatal("child was not joined")
	}
	if !checkpointFails && phase != "ignores-cancel" && !cmd.ProcessState.Success() {
		t.Error("cooperative child was not allowed to exit successfully")
	}
	if phase == "ignores-cancel" && cmd.ProcessState.ExitCode() == 4 {
		t.Error("child reached its emergency deadline instead of being stopped by the parent")
	}
	data, readErr := os.ReadFile(checkpoint)
	if checkpointFails || phase == "ignores-cancel" {
		if !os.IsNotExist(readErr) {
			t.Errorf("invalid checkpoint marker: %q %v", data, readErr)
		}
	} else if readErr != nil || string(data) != "saved" {
		t.Errorf("child stopped before saving checkpoint: %q %v", data, readErr)
	}
}

func TestWorkerParentPanicChildFixture(t *testing.T) {
	phase := os.Getenv("PCS_WORKER_PARENT_PANIC_CHILD")
	if phase == "" {
		return
	}
	fmt.Print("@pcsclone {\"Done\":128,\"Total\":1024}\n@pcsclone {\"Done\":256,\"Total\":1024}\n")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if scanner.Text() != "cancel" {
			continue
		}
		if phase == "ignores-cancel" {
			time.Sleep(8 * time.Second)
			os.Exit(4)
		}
		if strings.Contains(phase, "checkpoint") {
			fmt.Println(`@pcsclone {"CheckpointFailed":true}`)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Getenv("PCS_WORKER_PARENT_PANIC_CHECKPOINT"), []byte("saved"), 0600); err != nil {
			os.Exit(2)
		}
		fmt.Println(`@pcsclone {"Done":512,"Total":1024}`)
		os.Exit(0)
	}
	os.Exit(3)
}

func TestWorkerOutputDrainsControlFramesAfterCallbackPanic(t *testing.T) {
	cause := errors.New("synthetic batch callback panic")
	calls, stops := 0, 0
	ctx := context.WithValue(context.Background(), progressKey{}, func(transferProgress) { calls++; panic(cause) })
	output := &workerOutput{ctx: ctx, stop: func() { stops++ }}
	frames := []string{"@pcsclone {\"Done\":1}\n@pcsclone {\"Reset\":true}\n@pcsclone {\"Check", "pointFailed\":true}\n@pcsclone {\"Done\":2}\n"}
	for _, frame := range frames {
		if n, err := output.Write([]byte(frame)); err != nil || n != len(frame) {
			t.Fatalf("parser stopped draining: n=%d err=%v", n, err)
		}
	}
	if !errors.Is(output.callbackErr, cause) || calls != 1 || stops != 1 || !output.reset || !output.checkpointFailed || len(output.pending) != 0 {
		t.Fatalf("control frames lost after callback failure: callbacks=%d stops=%d reset=%t checkpoint=%t err=%v", calls, stops, output.reset, output.checkpointFailed, output.callbackErr)
	}
}
