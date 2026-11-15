package cliintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacySwitchUserExitAndPersistence(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "su fixture.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	for _, tc := range []struct {
		name    string
		args    []string
		input   string
		empty   bool
		wantUID uint64
	}{
		{name: "missing", args: []string{"absent"}},
		{name: "missing-uid", args: []string{"99"}},
		{name: "overflow-uid", args: []string{"18446744073709551616"}},
		{name: "empty-name", args: []string{""}},
		{name: "zero", args: []string{"0"}},
		{name: "too-many", args: []string{"Target", "extra"}},
		{name: "no-accounts", args: []string{"Target"}, empty: true},
		{name: "input-eof"},
		{name: "invalid-index", input: "invalid\n"},
		{name: "negative-index", input: "-1\n"},
		{name: "past-end-index", input: "3\n"},
		{name: "name", args: []string{"Target"}, wantUID: 8},
		{name: "case-insensitive", args: []string{"tArGeT"}, wantUID: 8},
		{name: "uid", args: []string{"8"}, wantUID: 8},
		{name: "numeric-name-first", args: []string{"7"}, wantUID: 9},
		{name: "first-index", input: "0\n", wantUID: 7},
		{name: "second-index", input: "1\n", wantUID: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			config := filepath.Join(dir, "pcs_config.json")
			original := []byte(`{"appid":1,"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"name":"Original","workdir":"/original"},{"uid":8,"name":"Target","workdir":"/target"},{"uid":9,"name":"7","workdir":"/numeric"}]}`)
			if tc.name == "overflow-uid" {
				original = bytes.Replace(original, []byte(`"uid":9`), []byte(`"uid":18446744073709551615`), 1)
			}
			if tc.empty {
				original = []byte(`{"appid":1,"baidu_active_uid":0,"baidu_user_list":[]}`)
			}
			if err := os.WriteFile(config, original, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, append([]string{"su"}, tc.args...)...)
			cmd.Dir = dir
			for _, e := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(e), "BAIDUPCS_GO_CONFIG_DIR=") {
					cmd.Env = append(cmd.Env, e)
				}
			}
			cmd.Env = childRaceEnv(t, append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+dir))
			cmd.Stdin = strings.NewReader(tc.input)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("timeout: %v", ctx.Err())
			}
			data, readErr := os.ReadFile(config)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantUID == 0 {
				exit, ok := err.(*exec.ExitError)
				if !ok || exit.ExitCode() != 1 || stderr.Len() == 0 || strings.Contains(stdout.String(), "切换用户失败") || strings.Contains(stdout.String(), "不能切换") || strings.Contains(stdout.String(), "切换用户:") {
					t.Errorf("failure status/streams: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
				if !bytes.Equal(data, original) {
					t.Error("rejected selection rewrote config")
				}
				return
			}
			if err != nil || stderr.Len() != 0 || !strings.Contains(stdout.String(), "切换用户:") {
				t.Fatalf("switch failed: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			var saved struct {
				UID   uint64 `json:"baidu_active_uid"`
				Users []struct {
					UID     uint64 `json:"uid"`
					Workdir string `json:"workdir"`
				} `json:"baidu_user_list"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.UID != tc.wantUID || len(saved.Users) != 3 || saved.Users[0].Workdir != "/original" || saved.Users[1].Workdir != "/target" || saved.Users[2].Workdir != "/numeric" {
				t.Errorf("wrong saved selection: %+v", saved)
			}
		})
	}
	t.Run("save-failure", func(t *testing.T) {
		dir := t.TempDir()
		config := filepath.Join(dir, "pcs_config.json")
		original := []byte(`{"appid":1,"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"name":"Original"},{"uid":8,"name":"Target"}]}`)
		if err := os.WriteFile(config, original, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "su")
		cmd.Dir = dir
		for _, e := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(e), "BAIDUPCS_GO_CONFIG_DIR=") {
				cmd.Env = append(cmd.Env, e)
			}
		}
		cmd.Env = childRaceEnv(t, append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+dir))
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		defer input.Close()
		output, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// The prompt means Reload has finished. Block publication before sending
		// the selection; no timing sleeps or changes to user-owned files.
		prompt := make(chan error, 1)
		outputDone := make(chan error, 1)
		go func() {
			buf := make([]byte, 256)
			notified := false
			for {
				n, err := output.Read(buf)
				stdout.Write(buf[:n])
				if !notified && strings.Contains(stdout.String(), "# 值 > ") {
					prompt <- nil
					notified = true
				}
				if err != nil {
					if !notified {
						prompt <- err
					}
					if err == io.EOF {
						err = nil
					}
					outputDone <- err
					return
				}
			}
		}()
		select {
		case err := <-prompt:
			if err != nil {
				cmd.Wait()
				t.Fatalf("prompt: %v", err)
			}
		case <-ctx.Done():
			cmd.Wait()
			t.Fatal("no account selection prompt")
		}
		retained := config + ".retained"
		if err := os.Rename(config, retained); err != nil {
			cancel()
			cmd.Wait()
			t.Fatal(err)
		}
		if err := os.Mkdir(config, 0700); err != nil {
			cancel()
			cmd.Wait()
			t.Fatal(err)
		}
		if _, err := io.WriteString(input, "1\n"); err != nil {
			cancel()
			cmd.Wait()
			t.Fatal(err)
		}
		input.Close()
		if err := <-outputDone; err != nil {
			cancel()
			cmd.Wait()
			t.Fatal(err)
		}
		err = cmd.Wait()
		if ctx.Err() != nil {
			t.Fatalf("timeout: %v", ctx.Err())
		}
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 1 || strings.Contains(stdout.String(), "切换用户:") || !strings.Contains(stderr.String(), "保存") {
			t.Errorf("failed save reported success: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
		}
		data, err := os.ReadFile(retained)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatalf("retained configuration changed: %v", err)
		}
		if info, err := os.Stat(config); err != nil || !info.IsDir() {
			t.Fatalf("publication blocker changed: %v", err)
		}
	})
}
