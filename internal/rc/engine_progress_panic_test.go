package rc

import (
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

func TestEngineProgressPanicStopsAndJoinsTransfers(t *testing.T) {
	for _, surface := range []string{"progress", "log"} {
		for _, typed := range []bool{false, true} {
			for _, move := range []bool{false, true} {
				mode := fmt.Sprintf("%s/%t/%t", surface, typed, move)
				t.Run(mode, func(t *testing.T) {
					t.Parallel()
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEngineProgressPanicFixture$")
					cmd.Env = append(os.Environ(), "PCS_ENGINE_PROGRESS_PANIC="+mode)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("engine progress panic escaped or finalization failed: %v %s", err, output)
					}
				})
			}
		}
	}
}

func TestEngineProgressPanicFixture(t *testing.T) {
	mode := os.Getenv("PCS_ENGINE_PROGRESS_PANIC")
	if mode == "" {
		return
	}
	parts := strings.Split(mode, "/")
	if len(parts) != 3 {
		t.Fatal("invalid fixture mode")
	}
	cloud := &fakeCloud{files: map[string][]byte{}, id: 101}
	source := cloud.endpoint(1, "/Temp")
	destination := Endpoint{&Local{}, t.TempDir()}
	plan := &Plan{Source: source, Destination: destination, Move: parts[2] == "true"}
	for i := 0; i < 6; i++ {
		name := fmt.Sprint(i)
		cloud.files[source.At(name)] = []byte("fixture")
		entry, err := source.FS.Stat(context.Background(), source.At(name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination.At(name), []byte("old destination"), 0600); err != nil {
			t.Fatal(err)
		}
		old, err := destination.FS.Stat(context.Background(), destination.At(name))
		if err != nil {
			t.Fatal(err)
		}
		plan.Actions = append(plan.Actions, Action{Kind: "copy", Path: name, Source: &entry, Destination: &old})
	}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	started := make(chan struct{}, 6)
	var calls, active, callbacks atomic.Int32
	runTransferWorker = func(ctx context.Context, _ string, _ uint64, _, _ string, _ int64) error {
		calls.Add(1)
		active.Add(1)
		defer active.Add(-1)
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	cause := errors.New("synthetic engine progress failure")
	raise := func() {
		callbacks.Add(1)
		for i := 0; i < 2; i++ {
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Error("active transfer did not start")
				return
			}
		}
		if parts[1] == "true" {
			panic(cause)
		}
		panic(cause.Error())
	}
	options := Options{Transfers: 2, ShowProgress: true}
	if parts[0] == "progress" {
		options.Progress = func(ProgressSnapshot) { raise() }
	} else {
		options.Emit = func(kind, _ string) {
			if kind == "progress" {
				raise()
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	result, err := Execute(ctx, plan, options)
	if ctx.Err() != nil {
		t.Fatalf("progress failure did not stop transfers before parent timeout: %v", err)
	}
	if !errors.Is(err, ErrProgressCallback) || !strings.Contains(err.Error(), cause.Error()) || parts[1] == "true" && !errors.Is(err, cause) {
		t.Fatalf("progress panic cause lost: %v", err)
	}
	if callbacks.Load() != 1 || calls.Load() != 2 || active.Load() != 0 || result.Failed != 2 || result.Canceled != 0 || result.Pending != 4 || result.Copied != 0 || result.Deleted != 0 {
		t.Fatalf("progress failure lost outcomes or left transfers running: result=%+v callbacks=%d calls=%d active=%d", result, callbacks.Load(), calls.Load(), active.Load())
	}
	for i := 0; i < 6; i++ {
		name := fmt.Sprint(i)
		if string(cloud.files[source.At(name)]) != "fixture" {
			t.Errorf("progress failure removed source %s", name)
		}
		if got, err := os.ReadFile(filepath.Join(destination.Root, name)); err != nil || string(got) != "old destination" {
			t.Errorf("progress failure replaced destination %s: %q %v", name, got, err)
		}
	}
}
