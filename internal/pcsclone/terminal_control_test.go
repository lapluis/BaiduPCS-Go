package pcsclone

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode"
)

func TestTerminalLogsDoNotRenderPathControls(t *testing.T) {
	for _, kind := range []string{"delete", "rmdir", "error", "summary"} {
		for control := rune(0); control <= 159; control++ {
			if control > 31 && control < 127 {
				continue
			}
			t.Run(fmt.Sprintf("%s/%02x", kind, control), func(t *testing.T) {
				var out bytes.Buffer
				sink := &logSink{w: &out, terminal: true, level: "INFO"}
				sink.Emit(kind, "名前/"+string(control)+"[2J-file+%20.mkv")
				if err := sink.Close(); err != nil {
					t.Fatal(err)
				}
				got := out.String()
				if strings.Count(got, "\n") != 1 || strings.ContainsFunc(strings.TrimSuffix(got, "\n"), unicode.IsControl) {
					t.Fatalf("path altered terminal output structure: %q", got)
				}
				if !strings.HasPrefix(got, kind+": 名前/") || !strings.Contains(got, "file+%20.mkv") {
					t.Fatalf("ordinary name characters changed: %q", got)
				}
			})
		}
	}
}

func TestJSONLogPreservesOriginalControlPath(t *testing.T) {
	const path = "名前/line\n\x1b[2J\u009b31m+%20.mkv"
	var out bytes.Buffer
	sink := &logSink{w: &out, json: true, level: "INFO"}
	sink.Emit("delete", path)
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	var record struct {
		Event string `json:"event"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(out.Bytes(), &record); err != nil || record.Event != "delete" || record.Path != path {
		t.Fatalf("JSON path changed: %q err=%v", out.String(), err)
	}
	if strings.Count(out.String(), "\n") != 1 || strings.ContainsRune(out.String(), '\x1b') {
		t.Fatalf("JSON contains an unescaped record separator or escape: %q", out.String())
	}
}
