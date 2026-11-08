package pcsclone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryOnlyFilterSingleFileTransfers(t *testing.T) {
	for _, mode := range []string{"copy", "move"} {
		for _, pattern := range []string{"*/", "**/secret/"} {
			t.Run(mode+"/"+pattern, func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "secret")
				target := filepath.Join(root, "target")
				if err := os.WriteFile(source, []byte("ordinary content"), 0600); err != nil {
					t.Fatal(err)
				}
				mustRun(t, filepath.Join(root, "config", defaultConfigName), mode, source, target, "--exclude", pattern)
				if data, err := os.ReadFile(filepath.Join(target, "secret")); err != nil || string(data) != "ordinary content" {
					t.Fatalf("ordinary file missing: %q %v", data, err)
				}
				data, err := os.ReadFile(source)
				if mode == "move" {
					if !os.IsNotExist(err) {
						t.Fatalf("move source retained: %v", err)
					}
				} else if err != nil || string(data) != "ordinary content" {
					t.Fatalf("copy source changed: %q %v", data, err)
				}
			})
		}
	}
}

func TestDirectoryOnlyIncludeListsDirectories(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(source, "parent", "secret"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "secret"), []byte("ordinary content"), 0600); err != nil {
		t.Fatal(err)
	}
	out := mustRun(t, filepath.Join(root, "config", defaultConfigName), "lsd", source, "-R", "--include", "**/secret/")
	if strings.TrimSpace(out) != "parent/secret/" {
		t.Fatalf("directory inclusion selected wrong entries: %q", out)
	}
}
