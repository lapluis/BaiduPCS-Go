package filelock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesProcessesAndReleasesAfterExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.lock")
	original := []byte("existing lock contents")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := TryLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	for _, busy := range []bool{true, false} {
		if !busy {
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLockProcessFixture$")
		expected := "free"
		if busy {
			expected = "busy"
		}
		cmd.Env = append(os.Environ(), "PCS_FILELOCK_FIXTURE="+path, "PCS_FILELOCK_EXPECT="+expected)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("process lock %s: %v %s", expected, err, out)
		}
	}
	// The child deliberately exited with an open handle. No PID recovery or
	// removal of the persistent pathname is necessary before acquiring again.
	next, err := TryLock(path)
	if err != nil {
		t.Fatal("child exit retained lock:", err)
	}
	next.Close()
	if data, err := os.ReadFile(path); err != nil || string(data) != string(original) {
		t.Fatal("locking changed file contents")
	}
}

func TestLockProcessFixture(t *testing.T) {
	path := os.Getenv("PCS_FILELOCK_FIXTURE")
	if path == "" {
		return
	}
	lease, err := TryLock(path)
	if os.Getenv("PCS_FILELOCK_EXPECT") == "busy" {
		if err == nil {
			lease.Close()
			t.Fatal("another process acquired held lock")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
