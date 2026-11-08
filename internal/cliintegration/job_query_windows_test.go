package cliintegration

import (
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestConsoleJobPIDQueryRetainsNativeOutputBuffer(t *testing.T) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConsoleJobPIDChildFixture$")
	cmd.Env = append(os.Environ(), "PCS_JOB_PID_CHILD=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	joined := false
	defer func() {
		if !joined {
			cancel()
			<-done
		}
	}()
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	err = windows.AssignProcessToJobObject(job, handle)
	windows.CloseHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	pids, err := consoleJobPIDs(job)
	if err != nil || len(pids) != 1 || !pids[uint32(cmd.Process.Pid)] {
		t.Fatalf("native process query lost child: pids=%v err=%v", pids, err)
	}
	input.Close()
	err = <-done
	joined = true
	if err != nil {
		t.Fatal(err)
	}
}

func TestConsoleJobPIDChildFixture(t *testing.T) {
	if os.Getenv("PCS_JOB_PID_CHILD") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}
