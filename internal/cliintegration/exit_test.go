package cliintegration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCLIExitStatus(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "pcs-test.exe")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, tc := range []struct {
		name string
		args []string
		fail bool
	}{
		{"help", []string{"--help"}, false},
		{"bad flag", []string{"--nonexistent-fixture-flag"}, true},
		{"bad option value", []string{"config", "set", "-max_download_rate", "invalid-fixture-value"}, true},
		{"bad upload rate", []string{"config", "set", "-max_upload_rate", "invalid-fixture-value"}, true},
		{"bad cache size", []string{"config", "set", "-cache_size", "invalid-fixture-value"}, true},
		{"bad server", []string{"config", "set", "-pcs_addr", "fixture.invalid"}, true},
		{"server suffix", []string{"config", "set", "-pcs_addr", "pcs.baidu.com.fixture.invalid"}, true},
		{"unknown command", []string{"unknown-fixture-command"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+t.TempDir())
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if tc.fail {
				if err == nil {
					t.Fatal("failed command returned success")
				}
				if _, ok := err.(*exec.ExitError); !ok {
					t.Fatal(err)
				}
				if stderr.Len() == 0 {
					t.Fatal("missing error on stderr")
				}
			} else if err != nil {
				t.Fatalf("successful command failed: %v\n%s", err, stderr.String())
			}
		})
	}
}
