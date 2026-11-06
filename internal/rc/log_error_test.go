package rc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

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

func TestLegacyLogFileFailures(t *testing.T) {
	for _, cmd := range []string{"copyto", "moveto", "mkdir", "deletefile"} {
		for _, mode := range []string{"normal", "write", "short", "close"} {
			for _, jsonLog := range []bool{false, true} {
				name := cmd + "/" + mode
				if jsonLog {
					name += "/json"
				}
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					previousDir := pcsconfig.GetConfigDir()
					pcsconfig.SetConfigDir(root)
					defer pcsconfig.SetConfigDir(previousDir)
					src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
					if err := os.WriteFile(src, []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
					failure := errors.New("fixture legacy log failure")
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
					args := []string{cmd}
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
					code := runWithIO(args, root, io.Discard, &stderr)
					if mode == "normal" {
						if code != 0 || stderr.Len() != 0 {
							t.Fatalf("normal legacy log failed: code=%d stderr=%q", code, stderr.String())
						}
					} else {
						want := failure.Error()
						if mode == "short" {
							want = io.ErrShortWrite.Error()
						}
						if code != 1 || !strings.Contains(stderr.String(), want) {
							t.Fatalf("legacy log failure hidden: code=%d stderr=%q", code, stderr.String())
						}
					}
					if f.closes != 1 {
						t.Fatalf("log closed %d times", f.closes)
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
							t.Fatalf("transfer changed: %q %v", data, err)
						}
						_, err = os.Stat(src)
						if cmd == "copyto" && err != nil || cmd == "moveto" && !os.IsNotExist(err) {
							t.Fatalf("source state changed: %v", err)
						}
					}
					if jsonLog && (mode == "normal" || mode == "close") {
						for _, line := range strings.Split(strings.TrimSpace(f.String()), "\n") {
							if !json.Valid([]byte(line)) {
								t.Fatalf("invalid legacy JSON log: %q", line)
							}
						}
					}
				})
			}
		}
	}
}

func TestLegacyLogClosePreservesOperationError(t *testing.T) {
	for _, mode := range []string{"write", "close", "usage-close"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			previousDir := pcsconfig.GetConfigDir()
			pcsconfig.SetConfigDir(root)
			defer pcsconfig.SetConfigDir(previousDir)
			failure := errors.New("fixture legacy log close")
			f := &faultLogFile{closeErr: failure}
			if mode == "write" {
				f.closeErr = nil
				f.writeErr = failure
			}
			previous := openLogFile
			openLogFile = func(string) (io.WriteCloser, error) { return f, nil }
			defer func() { openLogFile = previous }()
			var stderr bytes.Buffer
			args := []string{"copyto", filepath.Join(root, "missing"), filepath.Join(root, "target"), "--log-file", filepath.Join(root, "events.log")}
			wantCode := 1
			if mode == "usage-close" {
				args = []string{"copy", filepath.Join(root, "source"), filepath.Join(root, "target"), "--files-from", filepath.Join(root, "missing-list"), "--log-file", filepath.Join(root, "events.log")}
				wantCode = 2
			}
			code := runWithIO(args, root, io.Discard, &stderr)
			if code != wantCode || !strings.Contains(stderr.String(), "missing") || !strings.Contains(stderr.String(), failure.Error()) || f.closes != 1 {
				t.Fatalf("legacy diagnostics lost: code=%d stderr=%q closes=%d", code, stderr.String(), f.closes)
			}
		})
	}
}
