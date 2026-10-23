package pcsclone

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompletionAfterPositionals(t *testing.T) {
	tests := []struct {
		words []string
		want  string
	}{
		{[]string{"copy", "--ch"}, "--checksum"},
		{[]string{"copy", "source dir", "target dir", "--ch"}, "--checksum"},
		{[]string{"copy", "source", "--include", "*.txt", "target", "--ch"}, "--checksum"},
		{[]string{"--config", "config file", "copy", "source", "target", "--ch"}, "--checksum"},
		{[]string{"config", "rename", "old", "new", "--h"}, "--help"},
		{[]string{"baidu", "share", "create", "bd:/file", "--h"}, "--help"},
		{[]string{"baidu", "share", "cr"}, "create"},
		{[]string{"copy", "source", "target", "--", "--ch"}, ""},
		{[]string{"copy", "source", "target", "--include", "--ch"}, ""},
	}
	for _, shell := range []string{"powershell", "bash", "zsh"} {
		t.Run(shell, func(t *testing.T) {
			executable := shell
			if shell == "powershell" {
				executable = "pwsh"
			}
			resolved, err := exec.LookPath(executable)
			if shell == "bash" && runtime.GOOS == "windows" {
				gitBash := `C:\Program Files\Git\bin\bash.exe`
				if _, statErr := os.Stat(gitBash); statErr == nil {
					resolved = gitBash
					err = nil
				}
			}
			if err != nil {
				t.Skip("shell unavailable:", err)
			}
			script, err := completionScript(shell)
			if err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			if shell == "zsh" {
				b.WriteString("compdef() { :; }\n_describe() { print -rl -- ${candidates[@]}; }\n")
			}
			b.WriteString(script)
			for _, tc := range tests {
				if shell == "powershell" {
					words := append([]string{"pcsclone"}, tc.words...)
					quoted := make([]string, len(words))
					for i, w := range words {
						quoted[i] = w
						if strings.ContainsAny(w, " '") {
							quoted[i] = "'" + strings.ReplaceAll(w, "'", "''") + "'"
						}
					}
					line := strings.Join(quoted, " ")
					fmt.Fprintf(&b, "$line = '%s'\n", strings.ReplaceAll(line, "'", "''"))
					b.WriteString("$matches = @((TabExpansion2 $line $line.Length).CompletionMatches | ForEach-Object CompletionText)\n")
					if tc.want != "" {
						fmt.Fprintf(&b, "if ($matches -notcontains '%s') { throw \"Missing completion: $line : $matches\" }\n", tc.want)
					} else {
						b.WriteString("if ($matches -contains '--checksum') { throw \"Unexpected option: $line\" }\n")
					}
				} else {
					words := append([]string{"pcsclone"}, tc.words...)
					if shell == "bash" {
						fmt.Fprintf(&b, "COMP_WORDS=(%s)\nCOMP_CWORD=%d\n_pcsclone_completion\nresult=\"${COMPREPLY[*]}\"\n", quoteWords(words), len(words)-1)
					} else {
						fmt.Fprintf(&b, "words=(%s)\nCURRENT=%d\nresult=$(_pcsclone_completion)\n", quoteWords(words), len(words))
					}
					if tc.want != "" {
						fmt.Fprintf(&b, "[[ \"$result\" == *%s* ]] || { echo 'missing completion for %s'; exit 1; }\n", tc.want, strings.Join(tc.words, " "))
					} else {
						b.WriteString("[[ \"$result\" != *--checksum* ]] || exit 1\n")
					}
				}
			}
			file := filepath.Join(t.TempDir(), "completion-test.ps1")
			if shell != "powershell" {
				file = filepath.Join(t.TempDir(), "completion-test.sh")
			}
			if err = os.WriteFile(file, []byte(b.String()), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{file}
			if shell == "powershell" {
				args = []string{"-NoProfile", "-NonInteractive", "-File", file}
			}
			output, err := exec.Command(resolved, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
