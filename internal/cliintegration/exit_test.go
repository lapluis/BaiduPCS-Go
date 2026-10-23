package cliintegration

import (
	"bytes"
	"encoding/json"
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
		{"valid combined settings", []string{"config", "set", "-appid", "42", "-cache_size", "64KB", "-max_download_rate", "2MB", "-max_upload_rate", "1MB", "-pcs_addr", "c1.pcs.baidu.com"}, false},
		{"bad flag", []string{"--nonexistent-fixture-flag"}, true},
		{"bad option value", []string{"config", "set", "-max_download_rate", "invalid-fixture-value"}, true},
		{"bad upload rate", []string{"config", "set", "-max_upload_rate", "invalid-fixture-value"}, true},
		{"bad cache size", []string{"config", "set", "-cache_size", "invalid-fixture-value"}, true},
		{"bad server", []string{"config", "set", "-pcs_addr", "fixture.invalid"}, true},
		{"server suffix", []string{"config", "set", "-pcs_addr", "pcs.baidu.com.fixture.invalid"}, true},
		{"unknown command", []string{"unknown-fixture-command"}, true},
		{"partial rate update", []string{"config", "set", "-appid", "42", "-user_agent", "fixture-new", "-max_download_rate", "bad"}, true},
		{"partial upload update", []string{"config", "set", "-appid", "42", "-max_upload_rate", "bad"}, true},
		{"partial cache update", []string{"config", "set", "-appid", "42", "-cache_size", "bad"}, true},
		{"partial server update", []string{"config", "set", "-appid", "42", "-pcs_addr", "bad"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(binary, tc.args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			configDir := t.TempDir()
			configPath := filepath.Join(configDir, "pcs_config.json")
			if err := os.WriteFile(configPath, []byte(`{"appid":266719,"user_agent":"fixture-original"}`), 0600); err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+configDir)
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
				data, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				var config struct {
					AppID     int    `json:"appid"`
					UserAgent string `json:"user_agent"`
				}
				if err := json.Unmarshal(data, &config); err != nil {
					t.Fatal(err)
				}
				if config.AppID != 266719 || config.UserAgent != "fixture-original" {
					t.Fatal("failed command persisted partial settings")
				}
			} else if err != nil {
				t.Fatalf("successful command failed: %v\n%s", err, stderr.String())
			}
			if tc.name == "valid combined settings" {
				data, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]interface{}
				if err := json.Unmarshal(data, &config); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]interface{}{
					"appid": float64(42), "cache_size": float64(65536),
					"max_download_rate": float64(2 * 1024 * 1024), "max_upload_rate": float64(1024 * 1024),
					"pcs_addr": "c1.pcs.baidu.com",
				} {
					if config[key] != want {
						t.Errorf("%s was not applied: got %v, want %v", key, config[key], want)
					}
				}
			}
		})
	}
}
