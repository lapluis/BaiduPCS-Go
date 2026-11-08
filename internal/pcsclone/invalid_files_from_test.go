package pcsclone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidFilesFromCannotPartiallyApply(t *testing.T) {
	for _, command := range []string{"copy", "move", "sync", "delete"} {
		for _, tc := range []struct{ name, line, diagnostic string }{
			{"parent", "../outside.txt", "requires relative paths"},
			{"absolute", "/outside.txt", "requires relative paths"},
			{"backslash", `dir\outside.txt`, "requires relative paths"},
			{"oversized", strings.Repeat("x", 1024*1024+1), "token too long"},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
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
				list := filepath.Join(root, "selection.txt")
				if err := os.WriteFile(list, []byte("keep.txt\nextra.txt\n"+tc.line+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{command, source}
				if command != "delete" {
					args = append(args, target)
				}
				args = append(args, "--files-from", list)
				code, out, diagnostics := runCLI(t, filepath.Join(root, "config", defaultConfigName), args...)
				if code != 2 || out != "" || !strings.Contains(diagnostics, tc.diagnostic) {
					t.Errorf("invalid selection did not abort: code=%d out=%q stderr=%q", code, out, diagnostics)
				}
				for path, want := range originals {
					if data, err := os.ReadFile(path); err != nil || string(data) != want {
						t.Errorf("partial selection changed file: %s data=%q err=%v", path, data, err)
					}
				}
				if _, err := os.Stat(filepath.Join(target, "keep.txt")); !os.IsNotExist(err) {
					t.Errorf("partial selection copied a file: %v", err)
				}
			})
		}
	}
}
