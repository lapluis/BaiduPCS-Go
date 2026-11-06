package pcsclone

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogPathOverlapDoesNotModifyData(t *testing.T) {
	for _, cmd := range []string{"copyto", "moveto", "copy", "sync", "move", "check"} {
		for _, side := range []string{"source", "target"} {
			t.Run(cmd+"/"+side, func(t *testing.T) {
				root := t.TempDir()
				src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
				sourceFile, targetFile := src, dst
				directory := cmd != "copyto" && cmd != "moveto"
				if directory {
					if err := os.Mkdir(src, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(dst, 0700); err != nil {
						t.Fatal(err)
					}
					sourceFile, targetFile = filepath.Join(src, "one.txt"), filepath.Join(dst, "one.txt")
				}
				if err := os.WriteFile(sourceFile, []byte("source data"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(targetFile, []byte("target data"), 0600); err != nil {
					t.Fatal(err)
				}
				logPath := src
				if side == "target" {
					logPath = dst
				}
				if directory {
					logPath = filepath.Join(logPath, "events.log")
				}
				var stderr bytes.Buffer
				code := Run([]string{"--config", filepath.Join(root, "config", "pcs.json"), cmd, src, dst, "--log-file", logPath}, "test-version", io.Discard, &stderr)
				if code == 0 || !strings.Contains(stderr.String(), "log file overlaps") {
					t.Errorf("overlapping log accepted: %q", stderr.String())
				}
				for path, want := range map[string]string{sourceFile: "source data", targetFile: "target data"} {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != want {
						t.Errorf("log overlap changed %s: %q (%v)", path, data, err)
					}
				}
				if directory {
					if _, err := os.Stat(logPath); !os.IsNotExist(err) {
						t.Errorf("overlap created log inside data tree: %v", err)
					}
				}
			})
		}
	}
}

func TestLogPathProtectsMutationInputs(t *testing.T) {
	for _, cmd := range []string{"mkdir", "deletefile", "delete", "purge", "rmdir", "rmdirs"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%v", cmd, dry), func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "data")
				logPath := target
				if cmd == "deletefile" {
					if err := os.WriteFile(target, []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if cmd != "mkdir" {
						if err := os.Mkdir(target, 0700); err != nil {
							t.Fatal(err)
						}
					}
					logPath = filepath.Join(target, "events.log")
				}
				args := []string{"--config", filepath.Join(root, "config", "pcs.json"), cmd, target, "--log-file", logPath}
				if dry {
					args = append(args, "--dry-run")
				}
				var stderr bytes.Buffer
				if code := Run(args, "test-version", io.Discard, &stderr); code == 0 || !strings.Contains(stderr.String(), "log file overlaps") {
					t.Fatalf("overlap accepted: code=%d stderr=%q", code, stderr.String())
				}
				if cmd == "deletefile" {
					if data, err := os.ReadFile(target); err != nil || string(data) != "fixture" {
						t.Fatalf("file changed: %q %v", data, err)
					}
				} else {
					if _, err := os.Stat(logPath); !os.IsNotExist(err) {
						t.Fatalf("log created: %v", err)
					}
					_, err := os.Stat(target)
					if cmd == "mkdir" && !os.IsNotExist(err) || cmd != "mkdir" && err != nil {
						t.Fatalf("target changed: %v", err)
					}
				}
			})
		}
	}
}

func TestLogPathProtectsConfigAndFilter(t *testing.T) {
	for _, kind := range []string{"config", "aliases", "filter"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config", "pcs.json")
			if code := Run([]string{"--config", config, "mkdir", filepath.Join(root, "initial")}, "test-version", io.Discard, io.Discard); code != 0 {
				t.Fatal(code)
			}
			logPath := config
			if kind == "aliases" {
				logPath = filepath.Join(filepath.Dir(config), "rc_remotes.json")
			}
			if kind == "filter" {
				logPath = filepath.Join(root, "selection.txt")
			}
			if kind != "config" {
				data := "one.txt\n"
				if kind == "aliases" {
					data = "{}\n"
				}
				if err := os.WriteFile(logPath, []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
			if err := os.Mkdir(src, 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", config, "copy", src, dst, "--log-file", logPath}
			if kind == "filter" {
				args = append(args, "--files-from", logPath)
			}
			var stderr bytes.Buffer
			if code := Run(args, "test-version", io.Discard, &stderr); code == 0 || !strings.Contains(stderr.String(), "log file overlaps") {
				t.Fatalf("protected file accepted: code=%d %q", code, stderr.String())
			}
			if after, err := os.ReadFile(logPath); err != nil || !bytes.Equal(before, after) {
				t.Fatalf("protected file changed: %v", err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("transfer started: %v", err)
			}
		})
	}
}

func TestLogPathHardlinkAndIndependentLog(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("hardlinkConflict=%v", conflict), func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
			if err := os.Mkdir(src, 0700); err != nil {
				t.Fatal(err)
			}
			dataFile := filepath.Join(src, "one.txt")
			if err := os.WriteFile(dataFile, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			logPath := filepath.Join(root, "events.log")
			if conflict {
				if err := os.Link(dataFile, logPath); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(logPath, []byte("previous log\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			code := Run([]string{"--config", filepath.Join(root, "config", "pcs.json"), "copy", src, dst, "--log-file", logPath}, "test-version", io.Discard, &stderr)
			if conflict {
				if code == 0 || !strings.Contains(stderr.String(), "log file overlaps") {
					t.Fatalf("hardlink accepted: code=%d %q", code, stderr.String())
				}
				if _, err := os.Stat(dst); !os.IsNotExist(err) {
					t.Fatalf("transfer started: %v", err)
				}
			} else {
				if code != 0 {
					t.Fatalf("independent log rejected: code=%d %q", code, stderr.String())
				}
				if data, err := os.ReadFile(filepath.Join(dst, "one.txt")); err != nil || string(data) != "fixture" {
					t.Fatalf("copy failed: %q %v", data, err)
				}
				if data, err := os.ReadFile(logPath); err != nil || !bytes.HasPrefix(data, []byte("previous log\n")) || !bytes.Contains(data, []byte("copy:")) {
					t.Fatalf("append failed: %q %v", data, err)
				}
			}
			if data, err := os.ReadFile(dataFile); err != nil || string(data) != "fixture" {
				t.Fatalf("source changed: %q %v", data, err)
			}
		})
	}
}
