package pcsclone

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpAndCompletionErrorsDoNotEchoAttachedCredentialValues(t *testing.T) {
	const secret = "SYNTHETIC-DO-NOT-ECHO=VALUE"
	for _, credential := range []string{"bduss", "ptoken", "stoken", "cookies", "username", "password"} {
		option := "--" + credential + "=" + secret
		for _, tc := range []struct {
			name    string
			args    []string
			context string
		}{
			{"root-topic", []string{"help", option}, ""},
			{"nested-topic", []string{"help", "config", option}, "for config"},
			{"group-help", []string{"config", "help", option}, "for config"},
			{"baidu-help", []string{"baidu", "help", option}, "for baidu"},
			{"deep-topic", []string{"help", "baidu", "offline", option}, "for baidu offline"},
			{"deep-group-help", []string{"baidu", "offline", "help", option}, "for baidu offline"},
			{"command-after-separator", []string{"--", option}, ""},
			{"bare-topic", []string{"help", credential + "=" + secret}, ""},
			{"completion-after-separator", []string{"completion", "--", option}, ""},
			{"bare-completion", []string{"completion", credential + "=" + secret}, ""},
			{"leaf-topic-control", []string{"help", "config", "create", option}, `"config create" has no subcommands`},
			{"leaf-group-control", []string{"config", "help", "create", option}, `"config create" has no subcommands`},
		} {
			t.Run(credential+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				var out, diagnostics bytes.Buffer
				app := &App{CWD: root, Stdout: &out, Stderr: &diagnostics}
				args := append([]string{"--config", filepath.Join(root, defaultConfigName)}, tc.args...)
				code := app.run(args)
				if code != 2 || app.initialized || out.Len() != 0 || diagnostics.Len() == 0 {
					t.Errorf("help error behavior changed: code=%d initialized=%t out=%q stderr=%q", code, app.initialized, out.String(), diagnostics.String())
				}
				if strings.Contains(strings.ToLower(diagnostics.String()), "synthetic-do-not-echo") {
					t.Errorf("help diagnostic echoed attached value: %q", diagnostics.String())
				}
				if tc.context != "" && !strings.Contains(diagnostics.String(), tc.context) {
					t.Errorf("help error lost command context: want=%q stderr=%q", tc.context, diagnostics.String())
				}
				if !strings.HasPrefix(tc.name, "leaf-") && !strings.Contains(diagnostics.String(), credential) {
					t.Errorf("help error lost offending name: %q", diagnostics.String())
				}
			})
		}
	}
}

func TestHelpCommandPathsStillTraverseWithoutRuntime(t *testing.T) {
	for _, args := range [][]string{
		{"help", "config", "create"},
		{"config", "help", "create"},
		{"help", "baidu", "offline", "add"},
		{"baidu", "offline", "help", "add"},
		{"help", "baidu", "offline"},
		{"baidu", "offline", "help"},
	} {
		var out, diagnostics bytes.Buffer
		app := &App{CWD: t.TempDir(), Stdout: &out, Stderr: &diagnostics}
		if code := app.run(args); code != 0 || app.initialized || !strings.Contains(out.String(), "Usage:") || diagnostics.Len() != 0 {
			t.Fatalf("help path changed: args=%q code=%d initialized=%t out=%q stderr=%q", args, code, app.initialized, out.String(), diagnostics.String())
		}
		if strings.Join(args, " ") == "help baidu offline" || strings.Join(args, " ") == "baidu offline help" {
			if !strings.Contains(out.String(), "pcsclone help baidu offline <subcommand>") {
				t.Errorf("nested help suggested the wrong command path: %q", out.String())
			}
		}
	}
}
