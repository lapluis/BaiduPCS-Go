package rc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestObserverCancellationStopsWorkAndSurvivesFinalNotification(t *testing.T) {
	for _, phase := range []string{"execute-dry", "execute-copy", "check", "deletefile", "delete", "rmdirs", "cli-mkdir", "cli-copy"} {
		for _, timing := range []string{"before", "deadline", "first", "last"} {
			t.Run(phase+"/"+timing, func(t *testing.T) {
				sourceRoot, destinationRoot := t.TempDir(), t.TempDir()
				source, destination := Endpoint{&Local{}, sourceRoot}, Endpoint{&Local{}, destinationRoot}
				for _, name := range []string{"a", "b"} {
					if err := os.WriteFile(source.At(name), []byte("source-"+name), 0600); err != nil {
						t.Fatal(err)
					}
					if phase == "check" || phase == "deletefile" || phase == "delete" {
						if err := os.WriteFile(destination.At(name), []byte("source-"+name), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				if phase == "rmdirs" {
					for _, name := range []string{"one", "two"} {
						if err := os.Mkdir(destination.At(name), 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				wantErr := context.Canceled
				pre := timing == "before" || timing == "deadline"
				if timing == "before" {
					cancel()
				}
				if timing == "deadline" {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					wantErr = context.DeadlineExceeded
				}
				events := 2
				if phase == "execute-dry" {
					events = 4
				}
				if phase == "deletefile" || phase == "cli-mkdir" {
					events = 1
				}
				if phase == "cli-copy" {
					events = 3
				}
				cancelAt := 1
				if timing == "last" {
					cancelAt = events
				}
				calls := 0
				o := opts()
				o.Transfers, o.SizeOnly = 1, true
				o.Emit = func(string, string) {
					calls++
					if calls == cancelAt {
						cancel()
					}
				}
				var operationErr error
				switch phase {
				case "execute-dry", "execute-copy":
					o.DryRun = phase == "execute-dry"
					plan, err := BuildPlan(context.Background(), source, destination, "copy", o)
					if err != nil {
						t.Fatal(err)
					}
					plan.Move = o.DryRun
					_, operationErr = Execute(ctx, plan, o)
				case "check":
					operationErr = Check(ctx, source, destination, o)
				case "deletefile":
					operationErr = Delete(ctx, Endpoint{&Local{}, destination.At("a")}, "deletefile", o)
				case "delete", "rmdirs":
					operationErr = Delete(ctx, destination, phase, o)
				case "cli-mkdir", "cli-copy":
					args, command := []string{"made"}, "mkdir"
					if phase == "cli-copy" {
						args, command = []string{sourceRoot, destinationRoot}, "copy"
					}
					p, err := parse(args)
					if err != nil {
						t.Fatal(err)
					}
					p.Options, p.Progress = o, phase == "cli-copy"
					operationErr = runCommand(ctx, command, p, &Resolver{CWD: destinationRoot}, &bytes.Buffer{})
				}
				if !errors.Is(operationErr, wantErr) || errors.Is(operationErr, ErrLogCallback) || errors.Is(operationErr, ErrProgressCallback) {
					t.Errorf("observer cancellation hidden or reported as callback failure: %v", operationErr)
				}
				wantCalls := cancelAt
				if pre {
					wantCalls = 0
				}
				if phase == "cli-copy" && timing == "first" {
					wantCalls = 2
				} // The final summary remains available after cancellation.
				if calls != wantCalls {
					t.Errorf("continued notifications after cancellation: got=%d want=%d", calls, wantCalls)
				}
				for _, name := range []string{"a", "b"} {
					if got, err := os.ReadFile(source.At(name)); err != nil || string(got) != "source-"+name {
						t.Errorf("source changed: %q %v", got, err)
					}
				}
				if phase == "execute-copy" || phase == "cli-copy" {
					completed := 2
					if pre {
						completed = 0
					} else if timing == "first" {
						completed = 1
					}
					for i, name := range []string{"a", "b"} {
						data, err := os.ReadFile(destination.At(name))
						if i < completed {
							if err != nil || string(data) != "source-"+name {
								t.Errorf("completed output lost: %q %v", data, err)
							}
						} else if !errors.Is(err, os.ErrNotExist) {
							t.Errorf("unstarted output created: %s %v", name, err)
						}
					}
				}
				if phase == "check" || phase == "deletefile" || phase == "delete" {
					removed := 0
					if !pre && phase != "check" {
						removed = 1
						if phase == "delete" && timing == "last" {
							removed = 2
						}
					}
					for i, name := range []string{"a", "b"} {
						data, err := os.ReadFile(destination.At(name))
						if i < removed {
							if !errors.Is(err, os.ErrNotExist) {
								t.Errorf("completed removal lost: %s %v", name, err)
							}
						} else if err != nil || string(data) != "source-"+name {
							t.Errorf("later target changed: %q %v", data, err)
						}
					}
				}
				if phase == "rmdirs" {
					entries, err := os.ReadDir(destinationRoot)
					remaining := 2
					if !pre {
						remaining = 1
						if timing == "last" {
							remaining = 0
						}
					}
					if err != nil || len(entries) != remaining {
						t.Errorf("incorrect cleanup extent: entries=%d want=%d error=%v", len(entries), remaining, err)
					}
				}
				if phase == "cli-mkdir" {
					_, err := os.Stat(filepath.Join(destinationRoot, "made"))
					if pre && !errors.Is(err, os.ErrNotExist) || !pre && err != nil {
						t.Errorf("incorrect mkdir extent: %v", err)
					}
				}
				if phase == "execute-dry" {
					entries, err := os.ReadDir(destinationRoot)
					if err != nil || len(entries) != 0 {
						t.Errorf("dry run changed destination: %d %v", len(entries), err)
					}
				}
			})
		}
	}
}
