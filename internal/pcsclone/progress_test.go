package pcsclone

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func TestProgressTerminalAndRedirectedOutput(t *testing.T) {
	p := rc.ProgressSnapshot{Done: 512, Total: 1024, Rate: 128, ETA: 4 * time.Second, Active: 1, Count: 3, Files: []rc.FileProgress{{Path: strings.Repeat("日本語", 50) + "\x1b[2J", Done: 512, Total: 1024, Rate: 128}}}
	var terminal bytes.Buffer
	sink := &logSink{w: &terminal, terminal: true, level: "INFO"}
	sink.Progress(p)
	sink.Progress(p)
	sink.Emit("summary", "completed")
	sink.Close()
	if !strings.Contains(terminal.String(), "\x1b[") || !strings.Contains(terminal.String(), "50.0%") || strings.Contains(terminal.String(), "\x1b[2J") {
		t.Fatal("terminal progress missing or unsafe")
	}
	for _, line := range p.Lines() {
		if runewidth.StringWidth(line) > 78 {
			t.Fatal("progress wraps normal terminal width")
		}
	}
	var redirected bytes.Buffer
	log := &logSink{w: &redirected, level: "INFO", json: true}
	log.Progress(p)
	log.Progress(p)
	log.Emit("summary", "completed")
	lines := strings.Split(strings.TrimSpace(redirected.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("redirected progress not throttled: %q", redirected.String())
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) || strings.Contains(line, "\u001b") {
			t.Fatal("redirected log contains terminal controls")
		}
	}
}

func TestDownloadConcurrencyFlagsValidateBeforeTransfers(t *testing.T) {
	for _, args := range [][]string{{"--download-threads", "0"}, {"--download-threads", "33"}, {"--max-connections", "0"}, {"--transfers", "5", "--max-connections", "4"}} {
		_, flags, err := parseCommandFlags(append([]string{"source", "destination"}, args...), copyFlags())
		if err != nil {
			t.Fatal(err)
		}
		ctx := &commandContext{app: &App{Stderr: &bytes.Buffer{}}, flags: flags}
		if _, sink, err := ctx.options(); err == nil {
			sink.Close()
			t.Errorf("invalid connection settings accepted: %v", args)
		}
	}
}
