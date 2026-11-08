package rc

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestLegacyStringOptionValuesCannotConsumeFlags(t *testing.T) {
	for _, name := range []string{"include", "exclude", "files-from", "log-file", "log-level", "uid", "bwlimit"} {
		for _, next := range []string{"--dry-run", "-n", "--help", "--", "--typo=SYNTHETIC-NOT-FOR-OUTPUT"} {
			_, err := parse([]string{"--" + name, next})
			if err == nil || !strings.Contains(err.Error(), "missing value for --"+name) || strings.Contains(err.Error(), "SYNTHETIC-NOT-FOR-OUTPUT") {
				t.Errorf("option-like value consumed: %s %s err=%v", name, next, err)
			}
		}
	}
	for _, value := range []string{"--dry-run", "-draft*", "-"} {
		p, err := parse([]string{"--exclude=" + value, "--include=" + value, "--files-from=" + value, "--log-file=" + value, "--max-delete", "-1", "--depth", "-1"})
		if err != nil || len(p.Excludes) != 1 || p.Excludes[0] != value || len(p.Includes) != 1 || p.Includes[0] != value || p.FilesFrom != value || p.LogFile != value || p.MaxDelete != -1 || p.Depth != -1 {
			t.Fatalf("literal option value or negative number changed: %+v err=%v", p, err)
		}
	}
}

func TestLegacyMissingFilterValueCannotConsumeDryRun(t *testing.T) {
	previousDir := pcsconfig.GetConfigDir()
	pcsconfig.SetConfigDir(t.TempDir())
	defer pcsconfig.SetConfigDir(previousDir)
	for _, mode := range []string{"copy", "move", "sync", "delete"} {
		for _, flag := range []string{"--include", "--exclude"} {
			for _, dry := range []string{"--dry-run", "-n"} {
				t.Run(mode+"/"+flag+"/"+dry, func(t *testing.T) {
					source, target := endpoints(t)
					write(t, source, "keep.txt", "source retained")
					write(t, target, "extra.txt", "target retained")
					args := []string{mode, source.Root}
					if mode != "delete" {
						args = append(args, target.Root)
					}
					args = append(args, flag, dry)
					var out, diagnostics bytes.Buffer
					code := runWithIO(args, source.Root, &out, &diagnostics)
					if code != 2 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "missing value for "+flag) {
						t.Errorf("safety option swallowed: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
					}
					for name, want := range map[string]string{source.At("keep.txt"): "source retained", target.At("extra.txt"): "target retained"} {
						if data, err := os.ReadFile(name); err != nil || string(data) != want {
							t.Errorf("file changed: %s data=%q err=%v", name, data, err)
						}
					}
					if _, err := os.Stat(target.At("keep.txt")); !os.IsNotExist(err) {
						t.Errorf("invalid options copied file: %v", err)
					}
				})
			}
		}
	}
}
