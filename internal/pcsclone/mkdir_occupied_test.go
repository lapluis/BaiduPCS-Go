package pcsclone

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMkdirPreviewRejectsOccupiedTargetAndAncestor(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("ancestor=%t/dry=%t", ancestor, dry), func(t *testing.T) {
				root := t.TempDir()
				blocked := filepath.Join(root, "file")
				if err := os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				target := blocked
				if ancestor {
					target = filepath.Join(blocked, "missing", "child")
				}
				args := []string{"--config", filepath.Join(root, "config", "pcs.json"), "mkdir", target}
				if dry {
					args = append(args, "--dry-run")
				}
				var output, stderr bytes.Buffer
				app := &App{CWD: root, Version: "fixture", Stdout: &output, Stderr: &stderr}
				code := app.run(args)
				if code != 1 || strings.Contains(stderr.String(), "would-mkdir:") {
					t.Errorf("occupied target accepted: code=%d stderr=%q", code, stderr.String())
				}
				if data, err := os.ReadFile(blocked); err != nil || string(data) != "preserve" {
					t.Fatalf("occupied file changed: data=%q err=%v", data, err)
				}
			})
		}
	}
}
