package rc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestActionLogPanicsReturnErrorsAndStopFurtherWork(t *testing.T) {
	for _, phase := range []string{"dry-run", "copy", "move", "error", "delete", "deletefile", "dry-delete", "purge", "rmdir", "rmdirs", "check", "cli-mkdir", "dry-mkdir", "cli-summary", "cli-copy-log"} {
		for _, typed := range []bool{false, true} {
			mode := fmt.Sprintf("%s/%t", phase, typed)
			t.Run(mode, func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActionLogPanicFixture$")
				cmd.Env = append(os.Environ(), "PCS_ACTION_LOG_PANIC="+mode)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("action logging escaped or corrupted outcomes: %v %s", err, output)
				}
			})
		}
	}
}

func TestActionLogPanicFixture(t *testing.T) {
	mode := os.Getenv("PCS_ACTION_LOG_PANIC")
	if mode == "" {
		return
	}
	parts := strings.Split(mode, "/")
	phase := parts[0]
	sourceRoot, destinationRoot := t.TempDir(), t.TempDir()
	source, destination := Endpoint{&Local{}, sourceRoot}, Endpoint{&Local{}, destinationRoot}
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(source.At(name), []byte("source-"+name), 0600); err != nil {
			t.Fatal(err)
		}
		if phase == "delete" || phase == "deletefile" || phase == "dry-delete" || phase == "purge" || phase == "check" {
			if err := os.WriteFile(destination.At(name), []byte("target-"+name), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	cause := errors.New("synthetic action log failure")
	var calls atomic.Int32
	options := opts()
	options.Transfers = 1
	options.Emit = func(kind, _ string) {
		if phase == "cli-summary" && kind != "summary" {
			return
		}
		calls.Add(1)
		if parts[1] == "true" {
			panic(cause)
		}
		panic(cause.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var result Result
	var operationErr error
	switch phase {
	case "dry-run", "copy", "move":
		options.DryRun = phase == "dry-run"
		plan, err := BuildPlan(ctx, source, destination, "copy", options)
		if err != nil {
			t.Fatal(err)
		}
		plan.Move = phase == "move"
		result, operationErr = Execute(ctx, plan, options)
	case "error":
		blocked := filepath.Join(destinationRoot, "blocked")
		if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		plan := &Plan{Source: source, Destination: Endpoint{&Local{}, blocked}}
		for _, name := range []string{"a", "b"} {
			entry, err := source.FS.Stat(ctx, source.At(name))
			if err != nil {
				t.Fatal(err)
			}
			plan.Actions = append(plan.Actions, Action{Kind: "copy", Path: name, Source: &entry})
		}
		result, operationErr = Execute(ctx, plan, options)
		var pathErr *os.PathError
		if !errors.As(operationErr, &pathErr) {
			t.Errorf("original transfer error lost: %v", operationErr)
		}
	case "delete", "dry-delete", "purge":
		options.DryRun = phase == "dry-delete"
		deleteMode := "delete"
		if phase == "purge" {
			deleteMode = "purge"
		}
		operationErr = Delete(ctx, destination, deleteMode, options)
	case "deletefile":
		operationErr = Delete(ctx, Endpoint{&Local{}, destination.At("a")}, "deletefile", options)
	case "rmdir":
		operationErr = Delete(ctx, destination, "rmdir", options)
	case "rmdirs":
		for _, name := range []string{"one", "two"} {
			if err := os.Mkdir(destination.At(name), 0700); err != nil {
				t.Fatal(err)
			}
		}
		operationErr = Delete(ctx, destination, "rmdirs", options)
	case "check":
		operationErr = Check(ctx, source, destination, options)
	case "cli-mkdir", "dry-mkdir", "cli-summary", "cli-copy-log":
		args := []string{"made"}
		cmd := "mkdir"
		if phase == "cli-summary" || phase == "cli-copy-log" {
			args, cmd = []string{sourceRoot, destinationRoot}, "copy"
		}
		parsed, err := parse(args)
		if err != nil {
			t.Fatal(err)
		}
		parsed.Options = options
		parsed.DryRun = phase == "dry-mkdir"
		parsed.Progress = phase == "cli-summary" || phase == "cli-copy-log"
		operationErr = runCommand(ctx, cmd, parsed, &Resolver{CWD: destinationRoot}, &bytes.Buffer{})
	}
	if !errors.Is(operationErr, ErrLogCallback) || !strings.Contains(operationErr.Error(), cause.Error()) || parts[1] == "true" && !errors.Is(operationErr, cause) {
		t.Fatalf("action callback failure lost: %v", operationErr)
	}
	if calls.Load() != 1 {
		t.Errorf("continued logging after failure: %d calls", calls.Load())
	}
	if ctx.Err() != nil {
		t.Errorf("operation waited for parent timeout: %v", ctx.Err())
	}
	if phase == "copy" || phase == "move" {
		if result.Copied != 1 || result.Pending != 1 || result.Failed != 0 || result.Deleted != 0 {
			t.Errorf("completed copy mislabeled after log failure: %+v", result)
		}
		if got, err := os.ReadFile(destination.At("a")); err != nil || string(got) != "source-a" {
			t.Errorf("completed destination lost: %q %v", got, err)
		}
		if _, err := os.Stat(destination.At("b")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("queued copy started: %v", err)
		}
	}
	if phase == "error" && (result.Failed != 1 || result.Pending != 1) {
		t.Errorf("transfer failure count lost: %+v", result)
	}
	for _, name := range []string{"a", "b"} {
		if phase == "move" && name == "a" {
			if _, err := os.Stat(source.At(name)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("completed move source still present: %v", err)
			}
			continue
		}
		if got, err := os.ReadFile(source.At(name)); err != nil || string(got) != "source-"+name {
			t.Errorf("source changed: %q %v", got, err)
		}
	}
	if phase == "delete" || phase == "deletefile" || phase == "purge" || phase == "dry-delete" || phase == "check" {
		if got, err := os.ReadFile(destination.At("b")); err != nil || string(got) != "target-b" {
			t.Errorf("later action changed target b: %q %v", got, err)
		}
		if phase == "dry-delete" || phase == "check" {
			if got, err := os.ReadFile(destination.At("a")); err != nil || string(got) != "target-a" {
				t.Errorf("read-only operation changed a: %q %v", got, err)
			}
		} else if _, err := os.Stat(destination.At("a")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("completed deletion lost: %v", err)
		}
	}
	if phase == "rmdir" {
		if _, err := os.Stat(destinationRoot); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("completed rmdir lost: %v", err)
		}
	}
	if phase == "rmdirs" {
		entries, err := os.ReadDir(destinationRoot)
		if err != nil || len(entries) != 1 {
			t.Errorf("continued directory cleanup: entries=%d error=%v", len(entries), err)
		}
	}
	if phase == "cli-mkdir" || phase == "dry-mkdir" {
		_, err := os.Stat(destination.At("made"))
		if phase == "cli-mkdir" && err != nil || phase == "dry-mkdir" && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("mkdir outcome changed: %v", err)
		}
	}
	if phase == "cli-summary" {
		for _, name := range []string{"a", "b"} {
			if got, err := os.ReadFile(destination.At(name)); err != nil || string(got) != "source-"+name {
				t.Errorf("summary failure lost completed copy: %q %v", got, err)
			}
		}
	}
	if phase == "cli-copy-log" {
		if got, err := os.ReadFile(destination.At("a")); err != nil || string(got) != "source-a" {
			t.Errorf("completed CLI copy lost: %q %v", got, err)
		}
		if _, err := os.Stat(destination.At("b")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("CLI continued copying after log failure: %v", err)
		}
	}
}
