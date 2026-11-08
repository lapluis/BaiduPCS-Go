package pcsclone

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPowerShellCompletionUsesCursorPrefix(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell 7 unavailable:", err)
	}
	script, err := completionScript("powershell")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(script)
	for _, tc := range []struct {
		line, want string
	}{
		{"pcsclone <CURSOR>copy", "copy"},
		{"pcsclone baidu <CURSOR>share create", "share"},
		{"pcsclone baidu share <CURSOR>create bd:/file", "create"},
		{"pcsclone help ba<CURSOR> baidu share", "baidu"},
		{"pcsclone copy --include <CURSOR>--checksum", ""},
		{"pcsclone --config <CURSOR>copy", ""},
		{"pcsclone copy --<CURSOR>checksum", "--checksum"},
		{"pcsclone baidu share cr<CURSOR>", "create"},
	} {
		cursor := strings.Index(tc.line, "<CURSOR>")
		line := strings.Replace(tc.line, "<CURSOR>", "", 1)
		fmt.Fprintf(&b, "$line = '%s'\n", strings.ReplaceAll(line, "'", "''"))
		fmt.Fprintf(&b, "$matches = @((TabExpansion2 $line %d).CompletionMatches | ForEach-Object CompletionText)\n", cursor)
		if tc.want != "" {
			fmt.Fprintf(&b, "if ($matches -notcontains '%s') { throw \"Missing completion at offset %d: $line : $matches\" }\n", tc.want, cursor)
		} else {
			b.WriteString("if ($matches -contains 'copy' -or $matches -contains '--checksum') { throw \"Unexpected command/flag while completing a value: $line : $matches\" }\n")
		}
	}
	file := filepath.Join(t.TempDir(), "completion-cursor.ps1")
	if err := os.WriteFile(file, []byte(b.String()), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, pwsh, "-NoProfile", "-NonInteractive", "-File", file).CombinedOutput()
	if err != nil {
		t.Fatalf("PowerShell completion: %v\n%s", err, output)
	}
}
