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

func TestCanceledLocalListingCommandsDoNotWriteSuccess(t *testing.T) {
	for _, command := range []string{"lsjson", "size"} {
		for _, shape := range []string{"empty", "owned-state"} {
			t.Run(command+"/"+shape, func(t *testing.T) {
				root := t.TempDir()
				if shape == "owned-state" {
					if err := prepareStageDirectory(filepath.Join(root, ".pcsclone-partials")); err != nil {
						t.Fatal(err)
					}
				}
				p, err := parse([]string{root})
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				var output bytes.Buffer
				err = runCommand(ctx, command, p, &Resolver{CWD: root}, &output)
				if !errors.Is(err, context.Canceled) || output.Len() != 0 {
					t.Fatalf("canceled command wrote success: output=%q err=%v", output.String(), err)
				}
				output.Reset()
				if err := runCommand(context.Background(), command, p, &Resolver{CWD: root}, &output); err != nil {
					t.Fatal(err)
				}
				want := "[]\n"
				if command == "size" {
					want = "{\"bytes\":0,\"count\":0}\n"
				}
				if output.String() != want {
					t.Fatalf("healthy empty result changed: output=%q", output.String())
				}
			})
		}
	}
}

func TestLocalListingRejectsCanceledEmptyAndHiddenDirectories(t *testing.T) {
	for _, shape := range []string{"empty", "owned-state", "file"} {
		for _, deadline := range []bool{false, true} {
			t.Run(shape+"/"+map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
				root := t.TempDir()
				if shape == "owned-state" {
					if err := prepareStageDirectory(filepath.Join(root, ".pcsclone-partials")); err != nil {
						t.Fatal(err)
					}
				} else if shape == "file" {
					if err := os.WriteFile(filepath.Join(root, "file"), []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				want := context.Canceled
				if deadline {
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer cancel()
					want = context.DeadlineExceeded
				}
				got, err := (&Local{}).List(ctx, root)
				if !errors.Is(err, want) || got != nil {
					t.Fatalf("canceled local listing returned data: entries=%v err=%v", got, err)
				}
				for _, recursive := range []bool{false, true} {
					got, err = Listing(ctx, Endpoint{&Local{}, root}, recursive, -1, Filter{})
					if !errors.Is(err, want) || got != nil {
						t.Fatalf("canceled inventory succeeded: recursive=%t entries=%v err=%v", recursive, got, err)
					}
				}
			})
		}
	}
}

type cancelAfterListLocal struct {
	Local
	cancel context.CancelFunc
}

func (l *cancelAfterListLocal) List(ctx context.Context, p string) ([]Entry, error) {
	entries, err := l.Local.List(ctx, p)
	l.cancel()
	return entries, err
}

func TestListingRejectsCancellationAfterBackendReturns(t *testing.T) {
	for _, nonempty := range []bool{false, true} {
		root := t.TempDir()
		if nonempty {
			if err := os.WriteFile(filepath.Join(root, "file"), []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fs := &cancelAfterListLocal{cancel: cancel}
		got, err := Listing(ctx, Endpoint{fs, root}, true, -1, Filter{})
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("canceled backend result published: nonempty=%t entries=%v err=%v", nonempty, got, err)
		}
	}
}
