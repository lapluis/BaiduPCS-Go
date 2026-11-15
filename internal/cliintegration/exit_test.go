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
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	assertCLIRaceBuild(t, binary, false)
	t.Run("crypto batches", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("BAIDUPCS_GO_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), []byte(`{"appid":266719}`), 0600); err != nil {
			t.Fatal(err)
		}
		for _, batch := range []string{"all fail", "mixed", "success"} {
			t.Run(batch, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "input")
				missing := path + ".missing"
				payload := []byte("synthetic batch payload")
				if err := os.WriteFile(path, payload, 0600); err != nil {
					t.Fatal(err)
				}
				for _, op := range []string{"enc", "dec"} {
					args := []string{"tool", op, "-key=synthetic"}
					if batch != "success" {
						args = append(args, missing)
					}
					src := path
					if op == "dec" {
						src += ".encrypt"
					}
					if batch != "all fail" {
						args = append(args, src)
					}
					cmd := exec.Command(binary, args...)
					cmd.Env = childRaceEnv(t, os.Environ())
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					err := cmd.Run()
					if batch == "success" {
						if err != nil || stderr.Len() != 0 {
							t.Fatalf("%s: %v %s", op, err, &stderr)
						}
					} else if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 1 || !strings.Contains(stderr.String(), missing) {
						t.Fatalf("%s: %v %s", op, err, &stderr)
					}
					if strings.Contains(stdout.String(), "成功") != (batch != "all fail") {
						t.Fatalf("%s output: %s", op, &stdout)
					}
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("batch bytes: %v", err)
				}
			})
		}
	})
	for _, tc := range []struct {
		name string
		args []string
		fail bool
	}{
		{"help", []string{"--help"}, false},
		{"sparse accounts", []string{"loglist"}, false},
		{"switch sparse account", []string{"su"}, false},
		{"switch null account", []string{"su"}, true},
		{"logout without active account", []string{"logout", "-y"}, true},
		{"export output parent is a file", nil, true},
		{"invalid upload policy", nil, true},
		{"valid combined settings", []string{"config", "set", "-appid", "42", "-cache_size", "64KB", "-max_download_rate", "2MB", "-max_upload_rate", "1MB", "-pcs_addr", "c1.pcs.baidu.com"}, false},
		{"bad flag", []string{"--nonexistent-fixture-flag"}, true},
		{"bad option value", []string{"config", "set", "-max_download_rate", "invalid-fixture-value"}, true},
		{"bad upload rate", []string{"config", "set", "-max_upload_rate", "invalid-fixture-value"}, true},
		{"bad cache size", []string{"config", "set", "-cache_size", "invalid-fixture-value"}, true},
		{"bad server", []string{"config", "set", "-pcs_addr", "fixture.invalid"}, true},
		{"server suffix", []string{"config", "set", "-pcs_addr", "pcs.baidu.com.fixture.invalid"}, true},
		{"unknown command", []string{"unknown-fixture-command"}, true},
		{"partial rate update", []string{"config", "set", "-appid", "42", "-user_agent", "fixture-new", "-max_download_rate", "bad"}, true},
		{"malformed rate", []string{"config", "set", "-appid", "42", "-max_download_rate", "1.2.3MB"}, true},
		{"overflow rate", []string{"config", "set", "-appid", "42", "-max_upload_rate", "8192PB"}, true},
		{"invalid rate unit", []string{"config", "set", "-appid", "42", "-max_download_rate", "2MB/hour"}, true},
		{"partial upload update", []string{"config", "set", "-appid", "42", "-max_upload_rate", "bad"}, true},
		{"partial cache update", []string{"config", "set", "-appid", "42", "-cache_size", "bad"}, true},
		{"partial server update", []string{"config", "set", "-appid", "42", "-pcs_addr", "bad"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.name == "invalid upload policy" {
				args = []string{"upload", "-policy", "skpi", t.TempDir(), "/fixture"}
			}
			if tc.name == "export output parent is a file" {
				parent := filepath.Join(t.TempDir(), "blocking-file")
				if err := os.WriteFile(parent, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
				args = []string{"export", "-out", filepath.Join(parent, "child"), "/fixture.txt"}
			}
			cmd := exec.Command(binary, args...)
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			configDir := t.TempDir()
			configPath := filepath.Join(configDir, "pcs_config.json")
			fixture := `{"appid":266719,"user_agent":"fixture-original"}`
			if tc.name == "invalid upload policy" {
				fixture = `{"appid":266719,"user_agent":"fixture-original","u_policy":"overwrite"}`
			}
			if tc.name == "sparse accounts" || tc.name == "logout without active account" || strings.HasPrefix(tc.name, "switch ") {
				fixture = `{"appid":266719,"user_agent":"fixture-original","baidu_user_list":[null,{"uid":7,"name":"fixture-user"}]}`
			}
			if tc.name == "switch sparse account" {
				cmd.Stdin = strings.NewReader("1\n")
			}
			if tc.name == "switch null account" {
				cmd.Stdin = strings.NewReader("0\n")
			}
			if err := os.WriteFile(configPath, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+configDir)
			cmd.Env = childRaceEnv(t, cmd.Env)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if bytes.Contains(stderr.Bytes(), []byte("panic:")) {
				t.Fatal("CLI panicked")
			}
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
				if tc.name == "invalid upload policy" {
					if !strings.Contains(stderr.String(), "上传同名文件策略") {
						t.Errorf("invalid policy silently fell back: %s", stderr.String())
					}
					for _, name := range []string{"pcs_uploading.json", "pcs_uploading.json.lock"} {
						if _, err := os.Stat(filepath.Join(configDir, name)); !os.IsNotExist(err) {
							t.Errorf("invalid policy created resume state %s: %v", name, err)
						}
					}
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
			if tc.name == "switch sparse account" {
				data, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				var config struct {
					UID uint64 `json:"baidu_active_uid"`
				}
				if err := json.Unmarshal(data, &config); err != nil {
					t.Fatal(err)
				}
				if config.UID != 7 {
					t.Fatal("displayed sparse account index was not selected")
				}
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
