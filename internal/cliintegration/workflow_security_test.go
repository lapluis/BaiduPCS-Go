package cliintegration

import (
	"context"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func workflowSecurityFixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func workflowStep(t *testing.T, workflow, name string) string {
	t.Helper()
	start := strings.Index(workflow, "      - name: "+name+"\n")
	if start < 0 {
		t.Fatalf("missing workflow step %q", name)
	}
	step := workflow[start:]
	if next := strings.Index(step[1:], "\n      - "); next >= 0 {
		step = step[:next+1]
	}
	return step
}

func TestWorkflowReleasePolicy(t *testing.T) {
	workflow := workflowSecurityFixture(t)
	for _, required := range []string{
		"  LD_FLAGS: \"-w\"\n",
		"  GOTOOLCHAIN: local\n",
		"    needs: compatibility\n",
		"        include:\n          - goos: windows\n            goarch: arm\n            legacy: true\n",
		"hashFiles('go.mod', '.go-version', 'go.sum')",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("missing release invariant %q", required)
		}
	}
	if !strings.Contains(workflowStep(t, workflow, "Setup Go environment"), "go-version-file: ${{ matrix.legacy && 'go.mod' || '.go-version' }}") {
		t.Error("release targets do not use the pinned toolchain independently of compatibility builds")
	}
	if !strings.Contains(workflowStep(t, workflow, "Verify release artifacts"), "if: ${{ !matrix.legacy }}") {
		t.Error("legacy binaries must not be treated as patched release artifacts")
	}
	if !strings.Contains(workflowStep(t, workflow, "Upload artifact"), "if: ${{ success() && !github.head_ref && !matrix.legacy }}") {
		t.Error("upload must require success and exclude PRs and legacy builds")
	}
	build := strings.Index(workflow, "      - name: Build pcsclone binary\n")
	scan := strings.Index(workflow, "      - name: Verify release artifacts\n")
	upload := strings.Index(workflow, "      - name: Upload artifact\n")
	if build < 0 || !(build < scan && scan < upload) {
		t.Error("both binaries must be built and scanned before upload")
	}
	compat := strings.Index(workflow, "  compatibility:\n")
	if compat < 0 || !strings.Contains(workflow[compat:], "runs-on: windows-latest") || !strings.Contains(workflow[compat:], "go-version-file: go.mod") || !strings.Contains(workflow[compat:], "go test -p 1 ./...") || strings.Contains(workflow[compat:], "actions/upload-artifact") {
		t.Error("source compatibility must run on Windows without publishing old-SDK artifacts")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", ".go-version"))
	if err != nil {
		t.Fatal(err)
	}
	toolchain := "go" + strings.TrimSpace(string(data))
	// GO-2026-6859 (net/http) is fixed in Go 1.27.4.
	if !version.IsValid(toolchain) || version.Compare(toolchain, "go1.27.4") < 0 {
		t.Fatalf("release toolchain predates the verified security baseline: %q", toolchain)
	}
}

func TestWorkflowVulnerabilityGateStopsOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("workflow shell gate runs on Linux")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	script := workflowRunBlock(t, workflowSecurityFixture(t), "Verify release artifacts")
	for _, tc := range []struct {
		name, installCode, scanFailure string
		wantCode, wantScans            int
	}{
		{"success", "0", "", 0, 2},
		{"install-failure", "42", "", 42, 0},
		{"first-scan-failure", "0", "first", 3, 1},
		{"second-scan-failure", "0", "second", 3, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			for _, dir := range []string{bin, filepath.Join(root, "output")} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"first binary", "second binary"} {
				if err := os.WriteFile(filepath.Join(root, "output", name), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for name, source := range map[string]string{
				"go": `#!/bin/sh
set -eu
[ "$#" = 2 ]
[ "$1" = install ]
[ "$2" = golang.org/x/vuln/cmd/govulncheck@v1.9.0 ]
exit "$PCS_INSTALL_CODE"
`,
				"govulncheck": `#!/bin/sh
set -eu
[ "$#" = 3 ]
[ "$1" = -mode ]
[ "$2" = binary ]
[ -f "$3" ]
printf '%s\n' "$3" >> scans
case "$3:$PCS_SCAN_FAILURE" in
  'output/first binary:first'|'output/second binary:second') exit 3 ;;
esac
`,
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "-e", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "PCS_INSTALL_CODE="+tc.installCode, "PCS_SCAN_FAILURE="+tc.scanFailure)
			out, runErr := cmd.CombinedOutput()
			code := 0
			if runErr != nil {
				if exit, ok := runErr.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else {
					t.Fatal(runErr)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "scans"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if scans := strings.Count(string(data), "\n"); code != tc.wantCode || scans != tc.wantScans {
				t.Fatalf("gate continued or lost failure: code=%d want=%d scans=%d want=%d output=%s", code, tc.wantCode, scans, tc.wantScans, out)
			}
		})
	}
}
