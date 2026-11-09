package pcsclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func TestCancellationKeepsIndependentFailures(t *testing.T) {
	disk := &os.PathError{Op: "write", Path: "fixture.mkv", Err: errors.New("fixture disk full")}
	network := errors.New("fixture HTTP 403")
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"joined", errors.Join(disk, context.Canceled)},
		{"wrapped", fmt.Errorf("copy: %w", errors.Join(disk, context.Canceled))},
		{"multi-wrapped", fmt.Errorf("download fixture.mkv: %w; stopped: %w", disk.Err, context.Canceled)},
		{"nested", errors.Join(context.Canceled, errors.Join(network, disk))},
		{"checkpoint", errors.Join(context.Canceled, rc.ErrCheckpointSave, disk)},
		{"protocol", errors.Join(context.Canceled, rc.ErrWorkerProtocol, disk)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			app := &App{Stderr: &out}
			if code := app.reportCancellation(tc.err); code != 130 {
				t.Fatalf("exit=%d want=130", code)
			}
			text := out.String()
			if !strings.Contains(text, disk.Path) || !strings.Contains(text, disk.Err.Error()) || strings.Count(text, "pcsclone: canceled\n") != 1 {
				t.Fatalf("independent failure lost: %q", text)
			}
			if tc.name == "nested" && !strings.Contains(text, network.Error()) {
				t.Fatalf("second failure lost: %q", text)
			}
		})
	}
}

func TestOrdinaryCancellationDoesNotPrintPerFileErrors(t *testing.T) {
	for _, err := range []error{nil, context.Canceled, fmt.Errorf("fixture.mkv: %w", context.Canceled),
		errors.Join(fmt.Errorf("one: %w", context.Canceled), fmt.Errorf("two: %w", context.Canceled))} {
		var out bytes.Buffer
		app := &App{Stderr: &out}
		if code := app.reportCancellation(err); code != 130 || out.String() != "pcsclone: canceled\n" {
			t.Fatalf("ordinary cancellation printed errors: code=%d out=%q", code, out.String())
		}
	}
}

func TestCancellationKeepsCanceledLogFailure(t *testing.T) {
	file := &faultLogFile{closeErr: context.Canceled}
	logErr := (&logSink{w: file, file: file}).Close()
	if logErr == nil {
		t.Fatal("fixture did not fail closing its log")
	}
	for _, err := range []error{logErr, errors.Join(context.Canceled, logErr), fmt.Errorf("command: %w", logErr)} {
		var out bytes.Buffer
		app := &App{Stderr: &out}
		if code := app.reportCancellation(err); code != 130 || !strings.Contains(out.String(), logErr.Error()) {
			t.Fatalf("log failure hidden by cancellation: code=%d out=%q", code, out.String())
		}
	}
}
