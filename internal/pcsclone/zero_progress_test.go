package pcsclone

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZeroByteCopyAndSkipReportCompletedProgress(t *testing.T) {
	root := t.TempDir()
	src, dst := filepath.Join(root, "source"), filepath.Join(root, "target")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "zero"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"copy", "skip", "empty-plan"} {
		if phase == "empty-plan" {
			src = filepath.Join(root, "empty")
			if err := os.Mkdir(src, 0700); err != nil {
				t.Fatal(err)
			}
			dst = filepath.Join(root, "empty-target")
		}
		var log bytes.Buffer
		code := Run([]string{"--config", filepath.Join(root, "config", "pcs.json"), "copy", src, dst, "-P", "--use-json-log"}, "test", io.Discard, &log)
		if code != 0 {
			t.Fatalf("%s failed: code=%d log=%s", phase, code, log.String())
		}
		var progress, summary string
		for _, line := range strings.Split(strings.TrimSpace(log.String()), "\n") {
			var event struct{ Event, Path string }
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				t.Fatal(err)
			}
			if event.Event == "progress" {
				progress = event.Path
			}
			if event.Event == "summary" {
				summary = event.Path
			}
		}
		want := "copied=1 skipped=0"
		if phase == "skip" {
			want = "copied=0 skipped=1"
		} else if phase == "empty-plan" {
			want = "copied=0 skipped=0"
		}
		if !strings.HasPrefix(progress, "100.0% |") || !strings.HasPrefix(summary, want) {
			t.Errorf("%s incomplete progress: progress=%q summary=%q", phase, progress, summary)
		}
	}
	for _, side := range []string{"source", "target"} {
		info, err := os.Stat(filepath.Join(root, side, "zero"))
		if err != nil || !info.Mode().IsRegular() || info.Size() != 0 {
			t.Errorf("zero-byte file lost: side=%s err=%v", side, err)
		}
	}
}
