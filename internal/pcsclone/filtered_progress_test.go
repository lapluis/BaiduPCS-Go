package pcsclone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilteredSingleFileReportsFinalProgress(t *testing.T) {
	for _, command := range []string{"copy", "move"} {
		for _, selection := range []string{"exclude", "include", "files-from"} {
			for _, jsonLog := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/json=%t", command, selection, jsonLog), func(t *testing.T) {
					root := t.TempDir()
					src, dst := filepath.Join(root, "source.txt"), filepath.Join(root, "absent-target")
					if err := os.WriteFile(src, []byte("retained source"), 0600); err != nil {
						t.Fatal(err)
					}
					before, err := os.Stat(src)
					if err != nil {
						t.Fatal(err)
					}
					args := []string{"--config", filepath.Join(root, "config.json"), command, src, dst, "-P"}
					switch selection {
					case "exclude":
						args = append(args, "--exclude", "*")
					case "include":
						args = append(args, "--include", "other.txt")
					case "files-from":
						list := filepath.Join(root, "files.txt")
						if err := os.WriteFile(list, []byte("other.txt\n"), 0600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--files-from", list)
					}
					if jsonLog {
						args = append(args, "--use-json-log")
					}
					var log bytes.Buffer
					if code := Run(args, "test", io.Discard, &log); code != 0 {
						t.Fatalf("code=%d log=%s", code, log.String())
					}
					progress, summary := "", ""
					for _, line := range strings.Split(strings.TrimSpace(log.String()), "\n") {
						kind, item, _ := strings.Cut(line, ": ")
						if jsonLog && line != "" {
							var event struct{ Event, Path string }
							if err := json.Unmarshal([]byte(line), &event); err != nil {
								t.Fatal(err)
							}
							kind, item = event.Event, event.Path
						}
						if kind == "progress" {
							progress = item
						}
						if kind == "summary" {
							summary = item
						}
					}
					if !strings.HasPrefix(progress, "100.0% |") || !strings.Contains(progress, "files 0/0") || summary != "copied=0 skipped=0 deleted=0 failed=0 canceled=0 pending=0 bytes=0" {
						t.Errorf("filtered file lost final progress: %q summary=%q", progress, summary)
					}
					data, err := os.ReadFile(src)
					if err != nil || string(data) != "retained source" {
						t.Fatalf("source changed: %q %v", data, err)
					}
					after, err := os.Stat(src)
					if err != nil || !before.ModTime().Equal(after.ModTime()) {
						t.Fatalf("source timestamp changed: %v", err)
					}
					if _, err := os.Stat(dst); !os.IsNotExist(err) {
						t.Fatalf("filtered transfer created target: %v", err)
					}
				})
			}
		}
	}
}
