package rc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestLegacyInvalidFilesFromCannotPartiallyApply(t *testing.T) {
	previousDir := pcsconfig.GetConfigDir()
	pcsconfig.SetConfigDir(t.TempDir())
	defer pcsconfig.SetConfigDir(previousDir)
	for _, command := range []string{"copy", "move", "sync", "delete"} {
		for _, tc := range []struct{ name, line, diagnostic string }{
			{"parent", "../outside.txt", "requires relative paths"},
			{"absolute", "/outside.txt", "requires relative paths"},
			{"backslash", `dir\outside.txt`, "requires relative paths"},
			{"oversized", strings.Repeat("x", 1024*1024+1), "token too long"},
		} {
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				source, target := endpoints(t)
				write(t, source, "keep.txt", "source retained")
				write(t, target, "extra.txt", "target retained")
				list := filepath.Join(t.TempDir(), "selection.txt")
				if err := os.WriteFile(list, []byte("keep.txt\nextra.txt\n"+tc.line+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{command, source.Root}
				if command != "delete" {
					args = append(args, target.Root)
				}
				args = append(args, "--files-from", list)
				var out, diagnostics bytes.Buffer
				code := runWithIO(args, source.Root, &out, &diagnostics)
				if code != 2 || out.Len() != 0 || !strings.Contains(diagnostics.String(), tc.diagnostic) {
					t.Errorf("invalid selection did not abort: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
				}
				for path, want := range map[string]string{source.At("keep.txt"): "source retained", target.At("extra.txt"): "target retained"} {
					if data, err := os.ReadFile(path); err != nil || string(data) != want {
						t.Errorf("partial selection changed file: %s data=%q err=%v", path, data, err)
					}
				}
				if _, err := os.Stat(target.At("keep.txt")); !os.IsNotExist(err) {
					t.Errorf("partial selection copied a file: %v", err)
				}
			})
		}
	}
}
