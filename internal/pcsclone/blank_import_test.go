package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestImportRejectsExplicitBlankSource(t *testing.T) {
	for _, flags := range [][]string{{"--from", ""}, {"--from="}, {"--from", " \t "}, {"--from= \t"}} {
		t.Run(strings.Join(flags, ":"), func(t *testing.T) {
			root := t.TempDir()
			legacyDir := filepath.Join(root, "legacy")
			if err := os.MkdirAll(legacyDir, 0700); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(legacyDir, defaultConfigName)
			original := []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`)
			if err := os.WriteFile(legacy, original, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(pcsconfig.EnvConfigDir, legacyDir)
			destinationDir := filepath.Join(root, "destination")
			config := filepath.Join(destinationDir, defaultConfigName)
			var out, diagnostics bytes.Buffer
			app := &App{CWD: root, Stdout: &out, Stderr: &diagnostics}
			args := append([]string{"--config", config, "config", "import-pcsgo"}, flags...)
			code := app.run(args)
			if code != 2 || app.initialized || out.Len() != 0 || !strings.Contains(diagnostics.String(), "--from") {
				t.Errorf("blank source reached import: code=%d initialized=%t out=%q stderr=%q", code, app.initialized, out.String(), diagnostics.String())
			}
			if _, err := os.Stat(destinationDir); !os.IsNotExist(err) {
				t.Errorf("blank source created destination storage: %v", err)
			}
			if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, original) {
				t.Errorf("legacy source changed: data=%q err=%v", data, err)
			}
		})
	}
}

func TestImportSourceOmissionAndSpacedPath(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "omitted", true: "explicit"}[explicit], func(t *testing.T) {
			root := t.TempDir()
			legacyDir := filepath.Join(root, "legacy with spaces")
			if err := os.MkdirAll(legacyDir, 0700); err != nil {
				t.Fatal(err)
			}
			legacy := filepath.Join(legacyDir, defaultConfigName)
			original := []byte(`{"baidu_user_list":[{"uid":7,"name":"fixture","bduss":"synthetic-test-value"}]}`)
			if err := os.WriteFile(legacy, original, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(pcsconfig.EnvConfigDir, legacyDir)
			args := []string{"config", "import-pcsgo"}
			if explicit {
				args = append(args, "--from", legacy)
			}
			out := mustRun(t, filepath.Join(root, "destination", defaultConfigName), args...)
			if !strings.Contains(out, "Imported 1 account(s)") {
				t.Fatalf("valid source was not imported: %q", out)
			}
			if data, err := os.ReadFile(legacy); err != nil || !bytes.Equal(data, original) {
				t.Fatalf("legacy source changed: data=%q err=%v", data, err)
			}
		})
	}
}
