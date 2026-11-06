package rc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestValidateLogPath(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(dataDir, "nested", "one.txt")
	if err := os.Mkdir(filepath.Dir(data), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(data, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(root, "hardlink.log")
	if err := os.Link(data, hardlink); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "independent.log")
	if err := os.WriteFile(outside, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "other.log")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, log, endpoint string
		conflict            bool
	}{
		{"same file", data, data, true},
		{"inside source", filepath.Join(dataDir, "events.log"), dataDir, true},
		{"relative", "data/events.log", "./data", true},
		{"hardlink file", hardlink, data, true},
		{"hardlink descendant", hardlink, dataDir, true},
		{"new destination", filepath.Join(root, "new", "events.log"), filepath.Join(root, "new"), true},
		{"sibling prefix", filepath.Join(root, "data.log"), dataDir, false},
		{"independent hardlinks", outside, dataDir, false},
		{"remote", outside, "test:/data", false},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct {
			name, log, endpoint string
			conflict            bool
		}{"case alias", strings.ToUpper(data), data, true})
		cases = append(cases, struct {
			name, log, endpoint string
			conflict            bool
		}{"trailing dot alias", data + ".", data, true})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLogPath(context.Background(), root, tc.log, tc.endpoint)
			if (err != nil) != tc.conflict {
				t.Fatalf("conflict=%v: %v", tc.conflict, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateLogPath(ctx, root, outside, dataDir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if bytes, err := os.ReadFile(data); err != nil || string(bytes) != "fixture" {
		t.Fatalf("validation changed file: %q %v", bytes, err)
	}
}

func TestLegacyLogPathOverlap(t *testing.T) {
	for _, cmd := range []string{"copyto", "moveto", "copy", "sync", "move", "check", "mkdir", "deletefile", "delete", "purge", "rmdir", "rmdirs", "lsf"} {
		t.Run(cmd, func(t *testing.T) {
			root := t.TempDir()
			previousDir := pcsconfig.GetConfigDir()
			pcsconfig.SetConfigDir(filepath.Join(root, "config"))
			defer pcsconfig.SetConfigDir(previousDir)
			src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
			file := cmd == "copyto" || cmd == "moveto" || cmd == "deletefile"
			if file {
				if err := os.WriteFile(src, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if cmd != "mkdir" {
				if err := os.Mkdir(src, 0700); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{cmd, src}
			switch cmd {
			case "copyto", "moveto", "copy", "sync", "move", "check":
				args = append(args, dst)
			}
			logPath := src
			if !file {
				logPath = filepath.Join(src, "events.log")
			}
			args = append(args, "--log-file", logPath)
			var stderr bytes.Buffer
			if code := runWithIO(args, root, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "log file overlaps") {
				t.Fatalf("overlap accepted: code=%d %q", code, stderr.String())
			}
			if file {
				if data, err := os.ReadFile(src); err != nil || string(data) != "fixture" {
					t.Fatalf("source changed: %q %v", data, err)
				}
			} else {
				if _, err := os.Stat(logPath); !os.IsNotExist(err) {
					t.Fatalf("log created: %v", err)
				}
				_, err := os.Stat(src)
				if cmd == "mkdir" && !os.IsNotExist(err) || cmd != "mkdir" && err != nil {
					t.Fatalf("source changed: %v", err)
				}
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("destination created: %v", err)
			}
		})
	}
}

func TestValidateLogPathRejectsSymlinks(t *testing.T) {
	for _, parent := range []bool{false, true} {
		name := "file"
		if parent {
			name = "parent"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dataDir := filepath.Join(root, "data")
			if err := os.Mkdir(dataDir, 0700); err != nil {
				t.Fatal(err)
			}
			dataFile := filepath.Join(dataDir, "one.txt")
			if err := os.WriteFile(dataFile, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "alias")
			target, logPath := dataFile, link
			if parent {
				target, logPath = dataDir, filepath.Join(link, "one.txt")
			}
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.Errno(1314))) {
					t.Skip("symlink privilege unavailable")
				}
				t.Fatal(err)
			}
			if err := ValidateLogPath(context.Background(), root, logPath, dataDir); err == nil {
				t.Fatal("symlink log accepted")
			}
			if data, err := os.ReadFile(dataFile); err != nil || string(data) != "fixture" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		})
	}
}
