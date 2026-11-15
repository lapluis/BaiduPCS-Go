package pcsclone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func TestLogSinkRetainsOutputFailures(t *testing.T) {
	for _, kind := range []string{"text", "json", "progress", "clear"} {
		for _, short := range []bool{false, true} {
			name := kind
			if short {
				name += "/short"
			}
			t.Run(name, func(t *testing.T) {
				failure := errors.New("fixture log write failure")
				writer := &failingStdout{err: failure, short: short}
				sink := &logSink{w: writer, level: "INFO", json: kind == "json", terminal: kind == "progress" || kind == "clear"}
				if kind == "clear" {
					sink.frame = []string{"first", "second"}
				} else if kind == "progress" {
					sink.Progress(rc.ProgressSnapshot{Done: 1, Total: 2})
				} else {
					sink.Emit("copy", "fixture")
				}
				err := sink.Close()
				want := error(failure)
				if short {
					want = io.ErrShortWrite
				}
				if !errors.Is(err, want) {
					t.Fatalf("log write failure lost: err=%v writes=%d", err, writer.calls)
				}
			})
		}
	}
}

type faultLogFile struct {
	bytes.Buffer
	writeErr, closeErr error
	short              bool
	writes, closes     int
}

func (f *faultLogFile) Write(p []byte) (int, error) {
	f.writes++
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.short {
		return len(p) / 2, nil
	}
	return f.Buffer.Write(p)
}
func (f *faultLogFile) Close() error { f.closes++; return f.closeErr }

func TestLogSinkCloseOwnershipAndErrors(t *testing.T) {
	writeErr, closeErr := errors.New("fixture log write"), errors.New("fixture log close")
	f := &faultLogFile{writeErr: writeErr, closeErr: closeErr}
	sink := &logSink{w: f, file: f, level: "INFO"}
	sink.Emit("copy", "one")
	sink.Emit("copy", "two")
	err := sink.Close()
	if !errors.Is(err, writeErr) || !errors.Is(err, closeErr) || f.closes != 1 || f.writes != 1 {
		t.Fatalf("log errors lost: err=%v writes=%d closes=%d", err, f.writes, f.closes)
	}
	if again := sink.Close(); !errors.Is(again, writeErr) || !errors.Is(again, closeErr) || f.closes != 1 {
		t.Fatalf("repeated close changed result: %v closes=%d", again, f.closes)
	}
	sink.Emit("copy", "late")
	sink.Progress(rc.ProgressSnapshot{Done: 1, Total: 2})
	if f.writes != 1 {
		t.Fatal("closed log accepted writes")
	}
	borrowed := &faultLogFile{}
	if err := (&logSink{w: borrowed}).Close(); err != nil || borrowed.closes != 0 {
		t.Fatalf("borrowed writer closed: %v closes=%d", err, borrowed.closes)
	}
}

func TestLogFileFailureReachesCommand(t *testing.T) {
	for _, cmd := range []string{"copyto", "moveto", "mkdir", "deletefile"} {
		for _, mode := range []string{"normal", "write", "short", "close"} {
			for _, jsonLog := range []bool{false, true} {
				name := cmd + "/" + mode
				if jsonLog {
					name += "/json"
				}
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
					if err := os.WriteFile(src, []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
					failure := errors.New("fixture log failure")
					f := &faultLogFile{}
					switch mode {
					case "write":
						f.writeErr = failure
					case "short":
						f.short = true
					case "close":
						f.closeErr = failure
					}
					previous := openLogFile
					openLogFile = func(path string) (io.WriteCloser, error) {
						if path != filepath.Join(root, "events.log") {
							t.Errorf("log path changed: %s", path)
						}
						return f, nil
					}
					defer func() { openLogFile = previous }()
					args := []string{"--config", filepath.Join(root, "config.json"), cmd}
					switch cmd {
					case "copyto", "moveto":
						args = append(args, src, dst)
					case "mkdir":
						args = append(args, dst)
					case "deletefile":
						args = append(args, src)
					}
					args = append(args, "--log-file", filepath.Join(root, "events.log"))
					if jsonLog {
						args = append(args, "--use-json-log")
					}
					var stderr bytes.Buffer
					code := Run(args, "test-version", io.Discard, &stderr)
					if mode == "normal" {
						if code != 0 || stderr.Len() != 0 {
							t.Fatalf("normal log failed: code=%d stderr=%q", code, stderr.String())
						}
					} else {
						want := failure.Error()
						if mode == "short" {
							want = io.ErrShortWrite.Error()
						}
						if code != 1 || !strings.Contains(stderr.String(), want) {
							t.Fatalf("log failure hidden: code=%d stderr=%q", code, stderr.String())
						}
					}
					if f.closes != 1 {
						t.Fatalf("owned log closed %d times", f.closes)
					}
					if cmd == "mkdir" {
						if info, err := os.Stat(dst); err != nil || !info.IsDir() {
							t.Fatalf("mkdir result changed: %v", err)
						}
					} else if cmd == "deletefile" {
						if _, err := os.Stat(src); !os.IsNotExist(err) {
							t.Fatalf("delete result changed: %v", err)
						}
					} else {
						data, err := os.ReadFile(dst)
						if err != nil || string(data) != "fixture" {
							t.Fatalf("transfer result changed: %q %v", data, err)
						}
						_, err = os.Stat(src)
						if cmd == "copyto" && err != nil || cmd == "moveto" && !os.IsNotExist(err) {
							t.Fatalf("source state changed: %v", err)
						}
					}
					if jsonLog && (mode == "normal" || mode == "close") {
						for _, line := range strings.Split(strings.TrimSpace(f.String()), "\n") {
							if !json.Valid([]byte(line)) {
								t.Fatalf("invalid JSON log: %q", line)
							}
						}
					}
				})
			}
		}
	}
}

func TestLogCloseErrorPreservesOperationAndUsageDiagnostics(t *testing.T) {
	for _, usage := range []bool{false, true} {
		name := "operation"
		if usage {
			name = "usage"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			failure := errors.New("fixture log close failure")
			f := &faultLogFile{closeErr: failure}
			previous := openLogFile
			openLogFile = func(string) (io.WriteCloser, error) { return f, nil }
			defer func() { openLogFile = previous }()
			args := []string{"--config", filepath.Join(root, "config.json"), "copyto", filepath.Join(root, "missing"), filepath.Join(root, "target"), "--log-file", filepath.Join(root, "events.log")}
			wantCode, wantText := 1, "missing"
			if usage {
				args = append(args, "--transfers", "0")
				wantCode, wantText = 2, "invalid concurrency"
			}
			var stderr bytes.Buffer
			code := Run(args, "test-version", io.Discard, &stderr)
			if code != wantCode || !strings.Contains(stderr.String(), wantText) || !strings.Contains(stderr.String(), failure.Error()) || f.closes != 1 {
				t.Fatalf("combined diagnostics lost: code=%d stderr=%q closes=%d", code, stderr.String(), f.closes)
			}
		})
	}
}

func TestCancellationReportsLogCloseError(t *testing.T) {
	failure := errors.New("fixture canceled log close")
	f := &faultLogFile{closeErr: failure}
	logErr := (&logSink{w: f, file: f}).Close()
	for _, checkpoint := range []bool{false, true} {
		var stderr bytes.Buffer
		app := &App{Stderr: &stderr}
		err := errors.Join(context.Canceled, logErr)
		if checkpoint {
			err = errors.Join(err, rc.ErrCheckpointSave)
		}
		if code := app.reportCancellation(err); code != 130 || !strings.Contains(stderr.String(), failure.Error()) || !strings.Contains(stderr.String(), "canceled") {
			t.Fatalf("cancel diagnostics lost: code=%d stderr=%q", code, stderr.String())
		}
		if checkpoint && !strings.Contains(stderr.String(), rc.ErrCheckpointSave.Error()) {
			t.Fatalf("checkpoint failure lost: %q", stderr.String())
		}
	}
}
