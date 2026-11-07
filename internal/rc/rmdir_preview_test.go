package rc

import (
	"context"
	"fmt"
	"os"
	"testing"
)

func TestRmdirRechecksIdentityAfterListingForPreviewAndExecution(t *testing.T) {
	for _, mode := range []string{"unchanged", "replaced", "missing", "file"} {
		for _, dry := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/dry=%t", mode, dry), func(t *testing.T) {
				e, _ := endpoints(t)
				backend := &replaceDirectoryOnList{target: e.Root, replace: func() {
					switch mode {
					case "replaced":
						replaceEmptyDirectory(t, e.Root)
					case "missing", "file":
						if err := os.Remove(e.Root); err != nil {
							t.Fatal(err)
						}
						if mode == "file" {
							if err := os.WriteFile(e.Root, []byte("replacement file"), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
				}}
				e.FS = backend
				o := opts()
				o.DryRun = dry
				events := 0
				o.Emit = func(kind, name string) {
					want := "rmdir"
					if dry {
						want = "would-rmdir"
					}
					if kind != want || name != e.Root {
						t.Errorf("unexpected removal event: %s %s", kind, name)
					}
					events++
				}
				err := Delete(context.Background(), e, "rmdir", o)
				if !backend.fired {
					t.Fatal("listing mutation did not run")
				}
				if mode == "unchanged" {
					if err != nil || events != 1 {
						t.Fatalf("normal removal rejected: events=%d err=%v", events, err)
					}
				} else if err == nil || events != 0 {
					t.Errorf("changed target accepted: events=%d err=%v", events, err)
				}
				info, statErr := os.Stat(e.Root)
				switch {
				case mode == "file":
					if data, err := os.ReadFile(e.Root); err != nil || string(data) != "replacement file" {
						t.Fatalf("replacement file changed: data=%q err=%v", data, err)
					}
				case mode == "missing" || (mode == "unchanged" && !dry):
					if !os.IsNotExist(statErr) {
						t.Fatalf("removed directory exists: %v", statErr)
					}
				default:
					if statErr != nil || !info.IsDir() {
						t.Fatalf("retained directory changed: %v", statErr)
					}
				}
			})
		}
	}
}
