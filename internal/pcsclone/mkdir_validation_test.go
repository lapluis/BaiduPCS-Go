package pcsclone

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsMkdirDryRunValidatesEntireTarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows filename aliases")
	}
	for _, name := range []string{"bad.", "bad ", "CONIN$", "COM¹"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%t", name, dry), func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "target", name, "child")
				args := []string{"--config", filepath.Join(root, "config", "pcs.json"), "mkdir", target}
				if dry {
					args = append(args, "--dry-run")
				}
				var stderr bytes.Buffer
				code := Run(args, "test", io.Discard, &stderr)
				if code == 0 || !strings.Contains(stderr.String(), "Windows filename") {
					t.Fatal("mkdir accepted invalid target", code, stderr.String())
				}
				if _, err := os.Stat(filepath.Join(root, "target")); !os.IsNotExist(err) {
					t.Fatal("mkdir created part of invalid target", err)
				}
			})
		}
	}
}

func TestMkdirPreviewAndExecutionAgreeForValidNames(t *testing.T) {
	for _, name := range []string{"ordinary", ".hidden", "字幕"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%t", name, dry), func(t *testing.T) {
				root := t.TempDir()
				target := filepath.Join(root, "target", name, "child")
				args := []string{"--config", filepath.Join(root, "config", "pcs.json"), "mkdir", target}
				if dry {
					args = append(args, "--dry-run")
				}
				var stderr bytes.Buffer
				if code := Run(args, "test", io.Discard, &stderr); code != 0 {
					t.Fatal(code, stderr.String())
				}
				info, err := os.Stat(target)
				if dry {
					if !os.IsNotExist(err) {
						t.Fatal("preview created target", err)
					}
				} else if err != nil || !info.IsDir() {
					t.Fatal("valid mkdir did not create target", err)
				}
			})
		}
	}
}
