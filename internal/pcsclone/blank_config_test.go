package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalConfigKeepsPathsAndLiteralCommandArguments(t *testing.T) {
	const config = "space directory/settings.json"
	for _, flags := range [][]string{{"--config", config}, {"-c", config}, {"--config=" + config}} {
		args := append(append([]string{}, flags...), "copyto", "--", "--config", "-destination")
		g, err := parseGlobalOptions(args)
		if err != nil || g.config != config || len(g.args) != 4 || g.args[0] != "copyto" {
			t.Fatalf("nonempty config path or command changed: %+v err=%v", g, err)
		}
		paths, _, err := parseCommandFlags(g.args[1:], fileTransferFlags())
		if err != nil || len(paths) != 2 || paths[0] != "--config" || paths[1] != "-destination" {
			t.Fatalf("literal paths were parsed as options: paths=%q err=%v", paths, err)
		}
	}
}

func TestBlankExplicitConfigCannotModifyFallbackConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"long-empty", []string{"--config", ""}},
		{"short-empty", []string{"-c", ""}},
		{"long-whitespace", []string{"--config", " \t "}},
		{"short-whitespace", []string{"-c", "\n"}},
		{"equals-empty", []string{"--config="}},
		{"equals-whitespace", []string{"--config= \t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config.json")
			aliases := filepath.Join(root, "rc_remotes.json")
			originalConfig, originalAliases := []byte("{}"), []byte(`{"aa":7}`)
			for path, data := range map[string][]byte{config: originalConfig, aliases: originalAliases} {
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv(EnvConfigFile, config)
			var out, diagnostics bytes.Buffer
			app := &App{Version: "test", CWD: root, Stdout: &out, Stderr: &diagnostics}
			args := append(append([]string{}, tc.flags...), "config", "delete", "aa", "--keep-account")
			code := app.run(args)
			if code != 2 || app.initialized || out.Len() != 0 || !strings.Contains(diagnostics.String(), "missing value") {
				t.Errorf("blank config reached fallback operation: code=%d initialized=%t out=%q stderr=%q", code, app.initialized, out.String(), diagnostics.String())
			}
			for path, want := range map[string][]byte{config: originalConfig, aliases: originalAliases} {
				data, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(data, want) {
					t.Errorf("fallback file changed: %s data=%q err=%v", path, data, err)
				}
			}
		})
	}
}
