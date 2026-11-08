package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWorkerFinalProgressPreservesCancellation(t *testing.T) {
	for _, kind := range []string{"upload", "download"} {
		for _, checkpoint := range []bool{false, true} {
			for _, cancelOnProgress := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/checkpoint=%t/cancel=%t", kind, checkpoint, cancelOnProgress), func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerFinalProgressFixture$")
					cmd.Env = append(os.Environ(), "PCS_FINAL_PROGRESS_FIXTURE=1", fmt.Sprintf("PCS_FINAL_PROGRESS_CHECKPOINT=%t", checkpoint))
					calls := 0
					progressCtx := context.WithValue(ctx, progressKey{}, func(v transferProgress) {
						calls++
						if v.Done != 8 || v.Total != 8 || v.CheckpointFailed != checkpoint {
							t.Errorf("final progress changed: %+v", v)
						}
						if cancelOnProgress {
							cancel()
						}
					})
					err := executeWorkerCommand(progressCtx, cmd, kind, 7)
					if errors.Is(err, context.Canceled) != cancelOnProgress || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCheckpointSave) != checkpoint {
						t.Errorf("final progress lost error classification: %v", err)
					}
					if !cancelOnProgress && !checkpoint && err != nil {
						t.Errorf("healthy completed worker failed: %v", err)
					}
					if calls != 1 || cmd.ProcessState == nil || !cmd.ProcessState.Success() {
						t.Errorf("worker was not joined normally: callbacks=%d state=%v", calls, cmd.ProcessState)
					}
				})
			}
		}
	}
}

func TestWorkerFinalProgressFixture(t *testing.T) {
	if os.Getenv("PCS_FINAL_PROGRESS_FIXTURE") != "1" {
		return
	}
	// The EOF tail is parsed by output.finish after Cmd.Run has joined the
	// child. It must still be able to cancel an otherwise successful result.
	fmt.Printf("@pcsclone {\"Done\":8,\"Total\":8,\"CheckpointFailed\":%s}", os.Getenv("PCS_FINAL_PROGRESS_CHECKPOINT"))
	os.Exit(0)
}
