package rc

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyFilteredSingleFileFinalProgress(t *testing.T) {
	for _, command := range []string{"copy", "move"} {
		t.Run(command, func(t *testing.T) {
			root := t.TempDir()
			src, dst := filepath.Join(root, "source.txt"), filepath.Join(root, "absent-target")
			if err := os.WriteFile(src, []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			p, err := parse([]string{src, dst, "--exclude", "*", "-P"})
			if err != nil {
				t.Fatal(err)
			}
			p.Filter, err = NewFilter(nil, []string{"*"}, "")
			if err != nil {
				t.Fatal(err)
			}
			var final ProgressSnapshot
			summary := ""
			p.Options.Progress = func(snapshot ProgressSnapshot) { final = snapshot }
			p.Emit = func(kind, item string) {
				if kind == "summary" {
					summary = item
				}
			}
			if err := runCommand(context.Background(), command, p, &Resolver{CWD: root}, io.Discard); err != nil {
				t.Fatal(err)
			}
			if !final.Final || final.Count != 0 || final.percentage() != 100 || summary != (Result{}).String() {
				t.Errorf("filtered file lost final outcome: %+v summary=%q", final, summary)
			}
			if data, err := os.ReadFile(src); err != nil || string(data) != "retained" {
				t.Fatalf("source changed: %q %v", data, err)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Fatalf("target created: %v", err)
			}
		})
	}
}
