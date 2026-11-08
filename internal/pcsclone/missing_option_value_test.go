package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStringOptionValuesCannotConsumeFlags(t *testing.T) {
	specs := append(credentialFlags(), syncFlags()...)
	specs = append(specs, stringFlag("from", "", "FILE", ""))
	for _, spec := range specs {
		if spec.Kind != flagString && spec.Kind != flagList {
			continue
		}
		for _, next := range []string{"--dry-run", "-n", "--help", "--", "--typo=SYNTHETIC-NOT-FOR-OUTPUT"} {
			_, _, err := parseCommandFlags([]string{"--" + spec.Name, next}, specs)
			if err == nil || !strings.Contains(err.Error(), "missing value for --"+spec.Name) || strings.Contains(err.Error(), "SYNTHETIC-NOT-FOR-OUTPUT") {
				t.Errorf("option-like value consumed: %s %s err=%v", spec.Name, next, err)
			}
		}
		for _, value := range []string{"--dry-run", "-draft*", "-", "--password=synthetic=value"} {
			_, got, err := parseCommandFlags([]string{"--" + spec.Name + "=" + value}, specs)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Kind == flagList {
				if values := got.List(spec.Name); len(values) != 1 || values[0] != value {
					t.Errorf("literal list value changed: %s %v", spec.Name, values)
				}
			} else if got.String(spec.Name) != value {
				t.Errorf("literal string value changed: %s", spec.Name)
			}
		}
	}
	_, flags, err := parseCommandFlags([]string{"--max-delete", "-1", "--exclude", "-"}, specs)
	if err != nil || flags.Int("max-delete") != -1 || len(flags.List("exclude")) != 1 || flags.List("exclude")[0] != "-" {
		t.Fatalf("negative number or single dash rejected: flags=%v err=%v", flags, err)
	}
}

func TestGlobalConfigCannotConsumeFollowingFlag(t *testing.T) {
	for _, option := range []string{"--config", "-c"} {
		for _, next := range []string{"--help", "--version", "--", "--dry-run", "-n"} {
			var out, diagnostics bytes.Buffer
			app := &App{CWD: t.TempDir(), Stdout: &out, Stderr: &diagnostics}
			if code := app.run([]string{option, next, "version"}); code != 2 || app.initialized || out.Len() != 0 || !strings.Contains(diagnostics.String(), "missing value for "+option) {
				t.Errorf("config consumed following flag: %s %s code=%d initialized=%t out=%q stderr=%q", option, next, code, app.initialized, out.String(), diagnostics.String())
			}
		}
	}
	for _, value := range []string{"--help", "-settings.json", "-"} {
		g, err := parseGlobalOptions([]string{"--config=" + value, "version"})
		if err != nil || g.config != value {
			t.Fatalf("literal config value changed: %+v err=%v", g, err)
		}
	}
}

func TestMissingFilterValueCannotConsumeDryRun(t *testing.T) {
	for _, mode := range []string{"copy", "move", "sync", "delete"} {
		for _, flag := range []string{"--include", "--exclude"} {
			for _, dry := range []string{"--dry-run", "-n"} {
				t.Run(mode+"/"+flag+"/"+dry, func(t *testing.T) {
					root := t.TempDir()
					source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
					for _, dir := range []string{source, target} {
						if err := os.Mkdir(dir, 0700); err != nil {
							t.Fatal(err)
						}
					}
					originals := map[string]string{filepath.Join(source, "keep.txt"): "source retained", filepath.Join(target, "extra.txt"): "target retained"}
					for name, content := range originals {
						if err := os.WriteFile(name, []byte(content), 0600); err != nil {
							t.Fatal(err)
						}
					}
					args := []string{mode, source}
					if mode != "delete" {
						args = append(args, target)
					}
					args = append(args, flag, dry)
					code, out, diagnostics := runCLI(t, filepath.Join(root, "config", defaultConfigName), args...)
					if code != 2 || out != "" || !strings.Contains(diagnostics, "missing value for "+flag) {
						t.Errorf("safety option swallowed: code=%d out=%q stderr=%q", code, out, diagnostics)
					}
					if _, err := os.Stat(filepath.Join(root, "config")); !os.IsNotExist(err) {
						t.Errorf("invalid options initialized config: %v", err)
					}
					for name, want := range originals {
						if data, err := os.ReadFile(name); err != nil || string(data) != want {
							t.Errorf("file changed: %s data=%q err=%v", name, data, err)
						}
					}
					if _, err := os.Stat(filepath.Join(target, "keep.txt")); !os.IsNotExist(err) {
						t.Errorf("invalid options copied file: %v", err)
					}
				})
			}
		}
	}
}
