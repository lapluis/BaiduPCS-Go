package cliintegration

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBuildArchiveReplacesStaleEntriesAndPreservesPreviousOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX archive regression runs on Linux")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("zip"); err != nil {
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
		name string
		code int
	}{
		{"success", 0},
		{"partial-zip-failure", 43},
		{"rename-failure", 44},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			write("README.md", "current readme")
			write("out/fixture/BaiduPCS-Go", "current binary")
			var old bytes.Buffer
			writer := zip.NewWriter(&old)
			for _, name := range []string{"fixture/removed.txt", "fixture/BaiduPCS-Go", "unrelated.txt"} {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, "old content"); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			write("out/fixture.zip", old.String())
			// Keep the input directory; never perform a recursive shell deletion in tests.
			write("bin/rm", "#!/bin/sh\nif [ \"$1\" = -rf ]; then printf 'cleanup' > \"$BUILD_FIXTURE_ROOT/cleanup\"; fi\n")
			if tc.name == "partial-zip-failure" {
				write("bin/zip", `#!/bin/sh
printf 'incomplete stream'
# Simulate a failed tool that also damaged the archive path it was given.
if [ "$3" != - ]; then printf 'incomplete archive' > "$3"; fi
exit 43
`)
			}
			if tc.name == "rename-failure" {
				write("bin/mv", "#!/bin/sh\nexit 44\n")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, sh, "-c", prefix+"\nPack fixture\n")
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "BUILD_FIXTURE_ROOT="+root)
			output, runErr := cmd.CombinedOutput()
			if tc.code != 0 {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != tc.code {
					t.Fatalf("failure = %v, want exit %d: %s", runErr, tc.code, output)
				}
				got, err := os.ReadFile(filepath.Join(root, "out", "fixture.zip"))
				if err != nil || !bytes.Equal(got, old.Bytes()) {
					t.Errorf("previous archive changed on failure: %v", err)
				}
				if _, err := os.Stat(filepath.Join(root, "cleanup")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("source cleanup attempted after failure: %v", err)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("pack: %v\n%s", runErr, output)
			}
			archive, err := zip.OpenReader(filepath.Join(root, "out", "fixture.zip"))
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			got := map[string]string{}
			for _, file := range archive.File {
				if file.FileInfo().IsDir() {
					continue
				}
				r, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					t.Fatal(err)
				}
				got[file.Name] = string(content)
			}
			if len(got) != 2 || got["fixture/BaiduPCS-Go"] != "current binary" || got["fixture/README.md"] != "current readme" {
				t.Errorf("archive entries = %q; want only current binary and readme", got)
			}
		})
	}
}
