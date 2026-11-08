package cliintegration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildScriptStopsBeforePackagingOrCleanupOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX build script regression runs on Linux")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, found := strings.Cut(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n# Android\n")
	if !found {
		t.Fatal("build script function boundary missing")
	}
	for _, tc := range []struct {
		mode         string
		code         int
		zip, cleanup bool
		binary       string
	}{
		{"compile-failure", 42, false, false, "stale artifact"},
		{"zip-failure", 43, true, false, "fresh artifact"},
		{"success", 0, true, true, "fresh artifact"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			write := func(name, content string) {
				t.Helper()
				path := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("README.md", "fixture readme")
			write("out/fixture-first/BaiduPCS-Go", "stale artifact")
			write("bin/go", `#!/bin/sh
set -eu
printf 'go\n' >> "$BUILD_FIXTURE_ROOT/calls"
[ "$BUILD_FIXTURE_MODE" != compile-failure ] || exit 42
while [ "$1" != -o ]; do shift; done
printf 'fresh artifact' > "$2"
`)
			write("bin/zip", `#!/bin/sh
set -eu
printf 'zip\n' >> "$BUILD_FIXTURE_ROOT/calls"
[ "$BUILD_FIXTURE_MODE" != zip-failure ] || exit 43
printf 'archive'
`)
			// Record cleanup without performing recursive deletion, even in this fixture.
			write("bin/rm", "#!/bin/sh\nif [ \"$1\" = -rf ]; then printf 'cleanup\\n' >> \"$BUILD_FIXTURE_ROOT/calls\"; fi\n")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			script := prefix + `
old_golang() { go="$BUILD_FIXTURE_ROOT/bin/go"; }
Build fixture-first linux amd64
printf 'continued' > "$BUILD_FIXTURE_ROOT/continued"
`
			// Do not supply -e here: failure handling must come from build.sh itself.
			cmd := exec.CommandContext(ctx, sh, "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "BUILD_FIXTURE_ROOT="+root, "BUILD_FIXTURE_MODE="+tc.mode)
			output, runErr := cmd.CombinedOutput()
			code := 0
			if runErr != nil {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) {
					t.Fatalf("run: %v\n%s", runErr, output)
				}
				code = exitErr.ExitCode()
			}
			if code != tc.code {
				t.Errorf("exit code = %d, want %d\n%s", code, tc.code, output)
			}
			calls, err := os.ReadFile(filepath.Join(root, "calls"))
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := "go\n"
			if tc.zip {
				wantCalls += "zip\n"
			}
			if tc.cleanup {
				wantCalls += "cleanup\n"
			}
			if string(calls) != wantCalls {
				t.Errorf("calls = %q, want %q", calls, wantCalls)
			}
			for _, name := range []string{"continued", "out/fixture-first.zip"} {
				_, err := os.Stat(filepath.Join(root, name))
				if tc.code == 0 && err != nil {
					t.Errorf("missing successful output %s: %v", name, err)
				} else if tc.code != 0 && !errors.Is(err, os.ErrNotExist) {
					t.Errorf("unexpected output %s: %v", name, err)
				}
			}
			binary, err := os.ReadFile(filepath.Join(root, "out", "fixture-first", "BaiduPCS-Go"))
			if err != nil || string(binary) != tc.binary {
				t.Errorf("retained binary = %q, %v; want %q", binary, err, tc.binary)
			}
		})
	}
}
