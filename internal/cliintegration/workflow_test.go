package cliintegration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Run the actual workflow shell blocks with a recording compiler. This checks
// shell expansion and entry-point selection without downloading toolchains or
// pretending that a foreign executable was run on the host.
func TestWorkflowBuildArtifactNamesAndEntrypoints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("workflow shell regression runs on its Linux runner platform")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)
	if strings.Contains(workflow, "Build binary file") || strings.Contains(workflow, "baidupcs-go_") {
		t.Fatal("workflow must only build pcsclone release artifacts")
	}
	for _, target := range []struct{ os, arch string }{
		{"linux", "amd64"}, {"linux", "arm64"},
		{"windows", "amd64"}, {"windows", "arm64"},
		{"darwin", "amd64"}, {"darwin", "arm64"},
	} {
		for _, pr := range []string{"false", "true"} {
			for _, refType := range []string{"branch", "tag"} {
				t.Run(target.os+"/"+target.arch+"/pr="+pr+"/"+refType, func(t *testing.T) {
					root := t.TempDir()
					bin := filepath.Join(root, "bin")
					if err := os.Mkdir(bin, 0700); err != nil {
						t.Fatal(err)
					}
					const compiler = `#!/bin/sh
set -eu
[ "$CGO_ENABLED" = 0 ]
printf '%s\n' "$@" > go-arguments
[ "$1" = build ]
shift
[ "$1" = -o ]
output=$2
mkdir -p "${output%/*}"
printf 'fixture artifact\n' > "$output"
`
					if err := os.WriteFile(filepath.Join(bin, "go"), []byte(compiler), 0700); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, bash, "-e", "-c", workflowRunBlock(t, workflow, "Build pcsclone binary"))
					cmd.Dir = root
					cmd.Env = append(
						os.Environ(),
						"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
						"GOOS="+target.os,
						"GOARCH="+target.arch,
						"IS_PR="+pr,
						"PR_PROMPT=fixture PR notice",
						"LD_FLAGS=-w",
						"REF_TYPE="+refType,
						"REF_NAME=pcsclone-v0.1.0",
						"SHORT_SHA=1234567890abcdef",
					)
					output, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("workflow step failed: %v\n%s", err, output)
					}
					suffix := ""
					if target.os == "windows" {
						suffix = ".exe"
					}
					want := "pcsclone_" + target.os + "_" + target.arch + suffix
					files, err := os.ReadDir(filepath.Join(root, "output"))
					if err != nil || len(files) != 1 || files[0].Name() != want {
						t.Fatalf("wrong artifact: files=%v want=%s err=%v", files, want, err)
					}
					args, err := os.ReadFile(filepath.Join(root, "go-arguments"))
					if err != nil {
						t.Fatal(err)
					}
					wantVersion := "pcsclone-v0.1.0-dev+1234567"
					if refType == "tag" {
						wantVersion = "pcsclone-v0.1.0"
					}
					wantLDFlags := "-w -X main.Version=" + wantVersion
					got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
					if len(got) != 7 || got[0] != "build" || got[1] != "-o" || got[2] != "output/"+want || got[3] != "-trimpath" || got[4] != "-ldflags" || got[5] != wantLDFlags || got[6] != "./cmd/pcsclone" {
						t.Fatalf("wrong compiler invocation: %q", got)
					}
					if strings.Contains(string(output), "fixture PR notice") != (pr == "true") {
						t.Fatalf("PR notice changed: %q", output)
					}
				})
			}
		}
	}
}

func workflowRunBlock(t *testing.T, workflow, name string) string {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(workflow, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if line != "      - name: "+name {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "      - ") {
				break
			}
			if lines[j] != "        run: |" {
				continue
			}
			var script []string
			for k := j + 1; k < len(lines) && strings.HasPrefix(lines[k], "          "); k++ {
				script = append(script, strings.TrimPrefix(lines[k], "          "))
			}
			if len(script) == 0 {
				t.Fatalf("empty workflow script: %s", name)
			}
			return strings.Join(script, "\n")
		}
	}
	t.Fatalf("workflow run block missing: %s", name)
	return ""
}
