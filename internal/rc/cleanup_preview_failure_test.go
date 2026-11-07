package rc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDirectoryCleanupPreviewRejectsReplacedIdentity(t *testing.T) {
	for _, mode := range []string{"move", "rmdirs"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			if err := os.Mkdir(s.At("z"), 0700); err != nil {
				t.Fatal(err)
			}
			o := opts()
			o.DeleteEmptyDirs = true
			var plan *Plan
			var err error
			if mode == "move" {
				plan, err = BuildPlan(context.Background(), s, d, "move", o)
				if err != nil {
					t.Fatal(err)
				}
				replaceEmptyDirectory(t, s.At("z"))
			} else {
				s.FS = &replaceDirectoryOnList{target: s.Root, replace: func() { replaceEmptyDirectory(t, s.At("z")) }}
			}
			o.DryRun = true
			events := 0
			o.Emit = func(kind, _ string) {
				if kind == "would-rmdir" {
					events++
				}
			}
			if mode == "move" {
				_, err = Execute(context.Background(), plan, o)
			} else {
				err = Delete(context.Background(), s, "rmdirs", o)
			}
			if err == nil || !strings.Contains(err.Error(), "changed after planning") || events != 0 {
				t.Fatalf("preview accepted replacement: events=%d err=%v", events, err)
			}
			if info, err := os.Stat(s.At("z")); err != nil || !info.IsDir() {
				t.Fatalf("preview changed replacement directory: %v", err)
			}
		})
	}
}

func TestMoveCleanupPreviewStopsOnNotificationFailure(t *testing.T) {
	for _, mode := range []string{"cancel", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			for _, name := range []string{"a", "b"} {
				if err := os.Mkdir(s.At(name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			o := opts()
			o.DeleteEmptyDirs = true
			plan, err := BuildPlan(context.Background(), s, d, "move", o)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("fixture cleanup preview notification")
			events := 0
			o.DryRun = true
			o.Emit = func(kind, _ string) {
				if kind == "would-rmdir" {
					events++
					if mode == "cancel" {
						cancel()
					} else {
						panic(cause)
					}
				}
			}
			_, err = Execute(ctx, plan, o)
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrLogCallback) {
					t.Errorf("cancellation classification changed: %v", err)
				}
			} else if !errors.Is(err, ErrLogCallback) || !errors.Is(err, cause) {
				t.Errorf("notification failure lost: %v", err)
			}
			if events != 1 {
				t.Errorf("preview continued after failure: events=%d", events)
			}
			for _, name := range []string{"a", "b"} {
				if info, err := os.Stat(s.At(name)); err != nil || !info.IsDir() {
					t.Errorf("preview changed source directory %s: %v", name, err)
				}
			}
		})
	}
}
