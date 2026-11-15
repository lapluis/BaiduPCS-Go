package cliintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLegacyRunExitStatusAndStreams(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "run command.exe")
	build := exec.Command("go", cliBuildArgs("build", "-o", binary, ".")...)
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	assertCLIRaceBuild(t, binary, false)
	child := filepath.Join(binDir, "child fixture.exe")
	source := filepath.Join(binDir, "child.go")
	const program = `package main
import("encoding/json";"fmt";"io";"os";"strconv";"time")
func main(){
 if os.Args[1]=="io" {b,_:=json.Marshal(os.Args[2:]);fmt.Fprintln(os.Stdout,string(b));fmt.Fprintln(os.Stderr,"child-stderr");io.Copy(os.Stdout,os.Stdin);return}
 fmt.Fprintln(os.Stdout,"child-out");fmt.Fprintln(os.Stderr,"child-stderr")
 if os.Args[1]=="kill" {p,_:=os.FindProcess(os.Getpid());p.Kill();time.Sleep(time.Second);os.Exit(99)}
 code,_:=strconv.Atoi(os.Args[2]);os.Exit(code)
}
`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	childBuild := exec.Command("go", "build", "-o", child, source)
	childBuild.Dir = root
	if out, err := childBuild.CombinedOutput(); err != nil {
		t.Fatalf("child build: %v\n%s", err, out)
	}
	bad := filepath.Join(binDir, "invalid fixture.exe")
	if err := os.WriteFile(bad, []byte("fixture invalid executable\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"zero", "seven", "twenty-three", "missing", "invalid-format", "io", "killed", "help", "interactive"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "pcs_config.json"), []byte(`{"appid":1,"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"workdir":"/"}]}`), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"run", child, "exit", "0"}
			wantCode := 0
			input := ""
			values := []string{"--flag", "", "日本語 +%", "quote\" and backslash\\"}
			switch state {
			case "seven":
				args[len(args)-1] = "7"
				wantCode = 7
			case "twenty-three":
				args[len(args)-1] = "23"
				wantCode = 23
			case "missing":
				args = []string{"run", filepath.Join(binDir, "missing fixture.exe")}
				wantCode = 1
			case "invalid-format":
				args = []string{"run", bad}
				wantCode = 1
			case "io":
				args = append([]string{"run", child, "io"}, values...)
				input = "stdin fixture\nsecond line\n"
			case "killed":
				args = []string{"run", child, "kill"}
				wantCode = 137
				if runtime.GOOS == "windows" {
					wantCode = 1
				}
			case "help":
				args = []string{"run"}
			case "interactive":
				args = nil
				input = "run " + strconv.Quote(child) + " exit 7\npwd\nquit\n"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, args...)
			cmd.Dir = dir
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(strings.ToUpper(entry), "BAIDUPCS_GO_CONFIG_DIR=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = childRaceEnv(t, append(cmd.Env, "BAIDUPCS_GO_CONFIG_DIR="+dir))
			var stdout, stderr bytes.Buffer
			cmd.Stdin = strings.NewReader(input)
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("timeout: %v stdout=%s stderr=%s", ctx.Err(), stdout.String(), stderr.String())
			}
			gotCode := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					gotCode = exit.ExitCode()
				} else {
					t.Fatalf("run: %v", err)
				}
			}
			if gotCode != wantCode {
				t.Errorf("exit=%d want=%d stdout=%s stderr=%s", gotCode, wantCode, stdout.String(), stderr.String())
			}
			switch state {
			case "missing", "invalid-format":
				if stdout.Len() != 0 || stderr.Len() == 0 {
					t.Errorf("start failure not on stderr: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			case "io":
				b, _ := json.Marshal(values)
				if stdout.String() != string(b)+"\n"+input || stderr.String() != "child-stderr\n" {
					t.Errorf("arguments or streams changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			case "interactive":
				continued := false
				for _, line := range strings.Split(stdout.String(), "\n") {
					if strings.TrimPrefix(line, "BaiduPCS-Go > ") == "/" {
						continued = true
					}
				}
				if !strings.Contains(stdout.String(), "child-out") || !continued || !strings.Contains(stderr.String(), "exit status 7") {
					t.Errorf("interactive failure/continuation lost: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			case "help":
				if stdout.Len() == 0 {
					t.Error("run help missing")
				}
			default:
				if stdout.String() != "child-out\n" || !strings.HasPrefix(stderr.String(), "child-stderr\n") {
					t.Errorf("child output mixed with parent diagnostics: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
			}
		})
	}
}
