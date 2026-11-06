package rc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type faultLocalCopyOutput struct {
	*os.File
	writeErr, syncErr, closeErr error
	afterWrite                  func()
	syncs, closes               int
}

func (f *faultLocalCopyOutput) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	n, err := f.File.Write(p)
	if f.afterWrite != nil {
		f.afterWrite()
	}
	return n, err
}

func (f *faultLocalCopyOutput) Sync() error {
	f.syncs++
	if f.syncErr != nil {
		return f.syncErr
	}
	return f.File.Sync()
}

func (f *faultLocalCopyOutput) Close() error {
	f.closes++
	return errors.Join(f.File.Close(), f.closeErr)
}

func TestLocalCopyOutputErrorsRetainData(t *testing.T) {
	writeErr := errors.New("fixture local write failure")
	syncErr := errors.New("fixture local sync failure")
	closeErr := errors.New("fixture local close failure")
	for _, mode := range []struct {
		name                        string
		writeErr, syncErr, closeErr error
		cancel                      bool
	}{
		{name: "normal"},
		{name: "write", writeErr: writeErr},
		{name: "sync", syncErr: syncErr},
		{name: "close", closeErr: closeErr},
		{name: "write-and-close", writeErr: writeErr, closeErr: closeErr},
		{name: "sync-and-close", syncErr: syncErr, closeErr: closeErr},
		{name: "cancel-and-close", cancel: true, closeErr: closeErr},
	} {
		for _, command := range []string{"copy", "move", "sync"} {
			t.Run(command+"/"+mode.name, func(t *testing.T) {
				src, dst := endpoints(t)
				write(t, src, "one.txt", "complete source content")
				write(t, dst, "one.txt", "old target")
				write(t, dst, "extra.txt", "keep on failure")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				o := opts()
				o.Transfers = 1
				plan, err := BuildPlan(ctx, src, dst, command, o)
				if err != nil {
					t.Fatal(err)
				}
				var output *faultLocalCopyOutput
				previous := openLocalCopyOutput
				openLocalCopyOutput = func(name string) (localCopyOutput, error) {
					file, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0600)
					if err != nil {
						return nil, err
					}
					output = &faultLocalCopyOutput{File: file, writeErr: mode.writeErr, syncErr: mode.syncErr, closeErr: mode.closeErr}
					if mode.cancel {
						output.afterWrite = cancel
					}
					return output, nil
				}
				defer func() { openLocalCopyOutput = previous }()
				result, err := Execute(ctx, plan, o)
				normal := mode.name == "normal"
				if normal {
					if err != nil || result.Copied != 1 {
						t.Fatalf("normal copy failed: %+v %v", result, err)
					}
				} else {
					if err == nil || result.Copied != 0 {
						t.Errorf("output failure accepted: %+v %v", result, err)
					}
					for _, want := range []error{mode.writeErr, mode.syncErr, mode.closeErr} {
						if want != nil && !errors.Is(err, want) {
							t.Errorf("lost %v: %v", want, err)
						}
					}
					if mode.cancel && !errors.Is(err, context.Canceled) {
						t.Errorf("cancellation lost: %v", err)
					}
				}
				if output == nil || output.closes != 1 {
					t.Fatalf("output not closed exactly once: %+v", output)
				}
				wantSyncs := 1
				if mode.writeErr != nil || mode.cancel {
					wantSyncs = 0
				}
				if output.syncs != wantSyncs {
					t.Errorf("syncs=%d, want %d", output.syncs, wantSyncs)
				}
				wantTarget := "old target"
				if normal {
					wantTarget = "complete source content"
				}
				if data, err := os.ReadFile(dst.At("one.txt")); err != nil || string(data) != wantTarget {
					t.Errorf("target changed: %q %v", data, err)
				}
				if normal && command == "move" {
					if _, err := os.Stat(src.At("one.txt")); !os.IsNotExist(err) {
						t.Errorf("normal move retained source: %v", err)
					}
				} else if data, err := os.ReadFile(src.At("one.txt")); err != nil || string(data) != "complete source content" {
					t.Errorf("source lost: %q %v", data, err)
				}
				if normal && command == "sync" {
					if _, err := os.Stat(dst.At("extra.txt")); !os.IsNotExist(err) {
						t.Errorf("normal sync retained extra: %v", err)
					}
				} else if data, err := os.ReadFile(dst.At("extra.txt")); err != nil || string(data) != "keep on failure" {
					t.Errorf("extra lost: %q %v", data, err)
				}
				entries, err := os.ReadDir(dst.Root)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".pcs-rc-") {
						t.Errorf("temporary output leaked: %s", entry.Name())
					}
				}
			})
		}
	}
}
