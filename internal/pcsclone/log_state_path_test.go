package pcsclone

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogPathProtectsTransferState(t *testing.T) {
	for _, cmd := range []string{"copyto", "moveto"} {
		for _, kind := range []string{"owner", "payload", "ranges", "record", "completion", "lease", "upload", "legacy-upload", "legacy-upload-lock", "new-partial-log", "new-upload-log", "linked-partial", "linked-upload", "linked-legacy-upload"} {
			t.Run(cmd+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				configDir := filepath.Join(root, "config")
				config := filepath.Join(configDir, "pcs.json")
				if code := Run([]string{"--config", config, "mkdir", filepath.Join(root, "bootstrap")}, "test", io.Discard, io.Discard); code != 0 {
					t.Fatal(code)
				}
				src, dst := filepath.Join(root, "source.bin"), filepath.Join(root, "target.bin")
				if err := os.WriteFile(src, []byte("source fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dst, []byte("old target fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				partialDir := filepath.Join(root, ".pcsclone-partials")
				uploadDir := filepath.Join(configDir, "rc-state")
				for _, dir := range []string{partialDir, uploadDir} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				marker := []byte("pcsclone-owned-download-state-v1\n")
				if err := os.WriteFile(filepath.Join(partialDir, ".owner"), marker, 0600); err != nil {
					t.Fatal(err)
				}
				file := map[string]string{"owner": ".owner", "payload": "key.part", "ranges": "key.part.BaiduPCS-Go-downloading", "record": "key.json", "completion": "key.json.done", "lease": "key.lock"}[kind]
				if file == "" {
					file = "events.log"
				}
				state := filepath.Join(partialDir, file)
				switch kind {
				case "upload", "new-upload-log", "linked-upload":
					state = filepath.Join(uploadDir, "key.json")
				case "legacy-upload", "linked-legacy-upload":
					state = filepath.Join(configDir, "pcs_uploading.json")
				case "legacy-upload-lock":
					state = filepath.Join(configDir, "pcs_uploading.json.lock")
				}
				newLog := strings.HasPrefix(kind, "new-")
				before := []byte("synthetic resume state\n")
				if kind == "owner" {
					before = marker
				}
				if !newLog {
					if err := os.WriteFile(state, before, 0600); err != nil {
						t.Fatal(err)
					}
				}
				logPath := state
				if strings.HasPrefix(kind, "linked-") {
					logPath = filepath.Join(root, "external.log")
					if err := os.Link(state, logPath); err != nil {
						t.Fatal(err)
					}
				}
				srcArg, dstArg, logArg := src, dst, logPath
				if cmd == "moveto" {
					srcArg, dstArg = filepath.Base(src), filepath.Base(dst)
					var err error
					logArg, err = filepath.Rel(root, logPath)
					if err != nil {
						t.Fatal(err)
					}
				}
				var stderr bytes.Buffer
				app := &App{Version: "test", Stdout: io.Discard, Stderr: &stderr, CWD: root}
				code := app.run([]string{"--config", config, cmd, srcArg, dstArg, "--log-file", logArg})
				if code != 1 || !strings.Contains(stderr.String(), "log file overlaps") {
					t.Errorf("state log accepted: code=%d %q", code, stderr.String())
				}
				if newLog {
					if _, err := os.Stat(state); !os.IsNotExist(err) {
						t.Errorf("log created in state directory: %v", err)
					}
				} else if data, err := os.ReadFile(state); err != nil || !bytes.Equal(data, before) {
					t.Errorf("resume state changed: %q %v", data, err)
				}
				if data, err := os.ReadFile(src); err != nil || string(data) != "source fixture" {
					t.Errorf("source changed: %q %v", data, err)
				}
				if data, err := os.ReadFile(dst); err != nil || string(data) != "old target fixture" {
					t.Errorf("target changed: %q %v", data, err)
				}
			})
		}
	}
}
