package pcsclone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlankFilesFromCannotExpandOperation(t *testing.T) {
	for _, command := range []string{"copy", "move", "sync", "delete"} {
		for _, flags := range [][]string{{"--files-from", ""}, {"--files-from="}, {"--files-from", " \t "}, {"--files-from= \t"}} {
			t.Run(command+flags[0], func(t *testing.T) {
				root := t.TempDir()
				source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
				for _, dir := range []string{source, target} {
					if err := os.Mkdir(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
				originals := map[string]string{filepath.Join(source, "keep.txt"): "source retained", filepath.Join(target, "extra.txt"): "target retained"}
				for path, data := range originals {
					if err := os.WriteFile(path, []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
				args := []string{command, source}
				if command != "delete" {
					args = append(args, target)
				}
				args = append(args, flags...)
				code, _, diagnostics := runCLI(t, filepath.Join(root, "config", defaultConfigName), args...)
				if code != 2 || !strings.Contains(diagnostics, "missing value for --files-from") {
					t.Errorf("blank selection accepted: code=%d stderr=%q", code, diagnostics)
				}
				if _, err := os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
					t.Errorf("blank selection initialized configuration: %v", err)
				}
				for path, want := range originals {
					if data, err := os.ReadFile(path); err != nil || string(data) != want {
						t.Errorf("file changed: %s data=%q err=%v", path, data, err)
					}
				}
				if _, err := os.Stat(filepath.Join(target, "keep.txt")); !os.IsNotExist(err) {
					t.Errorf("unselected file copied: %v", err)
				}
			})
		}
	}
}

func TestEmptyFilesFromFileStillSelectsNothing(t *testing.T) {
	for _, command := range []string{"copy", "move", "sync", "delete"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
			for _, dir := range []string{source, target} {
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, dir := range []string{source, target} {
				if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			list := filepath.Join(root, "empty selection.txt")
			if err := os.WriteFile(list, nil, 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{command, source}
			if command != "delete" {
				args = append(args, target)
			}
			args = append(args, "--files-from", list)
			mustRun(t, filepath.Join(root, "config", defaultConfigName), args...)
			for _, dir := range []string{source, target} {
				if data, err := os.ReadFile(filepath.Join(dir, "keep.txt")); err != nil || string(data) != "retained" {
					t.Fatalf("empty list failed to protect file: %q err=%v", data, err)
				}
			}
		})
	}
}
