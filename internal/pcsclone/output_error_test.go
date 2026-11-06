package pcsclone

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingStdout struct {
	err   error
	short bool
	allow int
	calls int
}

func TestStdoutFailurePreservesReportedExitCode(t *testing.T) {
	previous := topLevelCommands()
	defer func() { allCommands = previous }()
	for _, want := range []int{1, 2, 130} {
		failure := errors.New("fixture stdout failure")
		allCommands = append(append([]*command(nil), previous...), &command{Name: "fixture-output", NoRuntime: true, Run: func(c *commandContext) error {
			_, _ = io.WriteString(c.app.Stdout, "partial output")
			return &exitError{code: want, err: errors.New("fixture command failure")}
		}})
		var stderr bytes.Buffer
		code := Run([]string{"fixture-output"}, "test-version", &failingStdout{err: failure}, &stderr)
		if code != want || !strings.Contains(stderr.String(), "fixture command failure") || strings.Contains(stderr.String(), failure.Error()) {
			t.Fatalf("existing exit was replaced: code=%d want=%d stderr=%q", code, want, stderr.String())
		}
	}
}

func TestStdoutWriterRestoredAfterRun(t *testing.T) {
	first := &failingStdout{err: errors.New("fixture stdout failure")}
	var stderr bytes.Buffer
	app := &App{Version: "test-version", Stdout: first, Stderr: &stderr, CWD: t.TempDir()}
	if code := app.run([]string{"version"}); code != 1 || app.Stdout != first {
		t.Fatalf("failed invocation changed output ownership: code=%d output=%T", code, app.Stdout)
	}
	var next bytes.Buffer
	app.Stdout = &next
	stderr.Reset()
	if code := app.run([]string{"version"}); code != 0 || next.String() != "pcsclone test-version\n" || stderr.Len() != 0 || app.Stdout != &next {
		t.Fatalf("output error leaked into next invocation: code=%d output=%q stderr=%q", code, next.String(), stderr.String())
	}
}

func (w *failingStdout) Write(p []byte) (int, error) {
	w.calls++
	if w.calls <= w.allow {
		return len(p), nil
	}
	if w.short {
		return len(p) / 2, nil
	}
	return 0, w.err
}

func TestStdoutFailureChangesSuccessfulExit(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "one.txt")
	if err := os.WriteFile(file, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	cases := [][]string{{"--version"}, {"version"}, {"--help"}, {"help"}, {"help", "copy"}, {"copy", "--help"}, {"baidu"}, {"config", "--help"}, {"help", "version"}, {"help", "help"}, {"completion", "bash"}, {"lsf", root}, {"lsjson", root}, {"size", root}, {"tree", root}, {"stat", file}}
	for _, args := range cases {
		for _, short := range []bool{false, true} {
			name := strings.Join(args, " ")
			if short {
				name += "/short"
			}
			t.Run(name, func(t *testing.T) {
				failure := errors.New("fixture stdout failure")
				writer := &failingStdout{err: failure, short: short}
				var stderr bytes.Buffer
				full := append([]string{"--config", filepath.Join(t.TempDir(), "config.json")}, args...)
				code := Run(full, "test-version", writer, &stderr)
				want := failure.Error()
				if short {
					want = io.ErrShortWrite.Error()
				}
				if code != 1 || !strings.Contains(stderr.String(), want) {
					t.Fatalf("stdout failure lost: code=%d stderr=%q calls=%d", code, stderr.String(), writer.calls)
				}
				if strings.Count(stderr.String(), want) != 1 {
					t.Fatalf("stdout failure reported repeatedly: %q", stderr.String())
				}
			})
		}
	}
}

func TestPartialStdoutFailureStopsFurtherWrites(t *testing.T) {
	failure := errors.New("fixture partial stdout failure")
	writer := &failingStdout{err: failure, allow: 1}
	var stderr bytes.Buffer
	code := Run([]string{"help"}, "test-version", writer, &stderr)
	if code != 1 || writer.calls != 2 || !strings.Contains(stderr.String(), failure.Error()) {
		t.Fatalf("partial output failure lost: code=%d calls=%d stderr=%q", code, writer.calls, stderr.String())
	}
}
