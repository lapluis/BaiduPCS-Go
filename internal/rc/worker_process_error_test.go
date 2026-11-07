package rc

import (
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

func TestWorkerProcessErrorsPreserveCause(t *testing.T) {
	for _, kind := range []string{"upload", "download"} {
		for _, mode := range []string{"missing", "not-executable", "exit", "canceled"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var cmd *exec.Cmd
				switch mode {
				case "missing", "not-executable":
					name := filepath.Join(t.TempDir(), "worker.exe")
					if mode == "not-executable" {
						if err := os.WriteFile(name, []byte("invalid executable fixture"), 0600); err != nil {
							t.Fatal(err)
						}
					}
					cmd = exec.CommandContext(ctx, name)
				default:
					cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWorkerProcessErrorFixture$")
					cmd.Env = append(os.Environ(), "PCS_PROCESS_ERROR_FIXTURE=1")
					if mode == "canceled" {
						cancel()
					}
				}
				err := executeWorkerCommand(ctx, cmd, kind, 7)
				if mode == "canceled" {
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancellation lost: %v", err)
					}
					return
				}
				if err == nil || !strings.Contains(err.Error(), kind+" transfer failed (account 7); source retained") {
					t.Fatalf("failure context lost: %v", err)
				}
				if strings.Contains(err.Error(), "fixture-private-diagnostic") || errors.Is(err, context.Canceled) || errors.Is(err, ErrCheckpointSave) || errors.Is(err, ErrWorkerProtocol) {
					t.Fatalf("private diagnostics leaked or failure misclassified: %v", err)
				}
				if mode == "exit" {
					var exitErr *exec.ExitError
					if !errors.As(err, &exitErr) || exitErr.ExitCode() != 19 || len(exitErr.Stderr) != 0 || cmd.ProcessState == nil {
						t.Fatalf("exit cause lost or child not joined: %v", err)
					}
				} else {
					var pathErr *os.PathError
					if !errors.As(err, &pathErr) || mode == "missing" && !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("startup cause lost: %v", err)
					}
				}
			})
		}
	}
}

func TestWorkerProcessErrorFixture(t *testing.T) {
	if os.Getenv("PCS_PROCESS_ERROR_FIXTURE") != "1" {
		return
	}
	fmt.Println("fixture-private-diagnostic stdout")
	fmt.Fprintln(os.Stderr, "fixture-private-diagnostic stderr")
	os.Exit(19)
}
