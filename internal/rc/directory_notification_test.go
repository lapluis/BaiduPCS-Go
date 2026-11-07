package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryDeletionStopsAfterNotificationFailure(t *testing.T) {
	for _, mode := range []string{"rmdir", "rmdirs", "purge"} {
		for _, dry := range []bool{false, true} {
			for _, failure := range []string{"cancel", "panic", "cancel-and-panic"} {
				t.Run(fmt.Sprintf("%s/dry=%t/%s", mode, dry, failure), func(t *testing.T) {
					root := filepath.Join(t.TempDir(), "root")
					if err := os.Mkdir(root, 0700); err != nil {
						t.Fatal(err)
					}
					e := Endpoint{FS: &Local{}, Root: root}
					if mode != "rmdir" {
						for _, name := range []string{"a/b", "c"} {
							if err := os.MkdirAll(e.At(name), 0700); err != nil {
								t.Fatal(err)
							}
						}
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					cause := errors.New("directory notification fixture failure")
					o := opts()
					o.DryRun = dry
					calls := 0
					o.Emit = func(kind, item string) {
						calls++
						wantKind, wantPath := "rmdir", root
						if dry {
							wantKind = "would-rmdir"
						}
						if mode != "rmdir" {
							wantPath = "a/b"
						}
						if kind != wantKind || item != wantPath {
							t.Errorf("unexpected first notification: kind=%q path=%q", kind, item)
						}
						if failure != "panic" {
							cancel()
						}
						if failure != "cancel" {
							panic(cause)
						}
					}
					err := Delete(ctx, e, mode, o)
					if calls != 1 {
						t.Errorf("continued notifications: calls=%d err=%v", calls, err)
					}
					if got, want := errors.Is(err, context.Canceled), failure != "panic"; got != want {
						t.Errorf("cancellation classification: got=%t want=%t err=%v", got, want, err)
					}
					if got, want := errors.Is(err, ErrLogCallback), failure != "cancel"; got != want {
						t.Errorf("callback classification: got=%t want=%t err=%v", got, want, err)
					}
					if got, want := errors.Is(err, cause), failure != "cancel"; got != want {
						t.Errorf("callback cause: got=%t want=%t err=%v", got, want, err)
					}
					paths := []string{""}
					if mode != "rmdir" {
						paths = []string{"", "a", "a/b", "c"}
					}
					for _, name := range paths {
						info, statErr := os.Stat(e.At(name))
						removed := !dry && ((mode == "rmdir" && name == "") || (mode != "rmdir" && name == "a/b"))
						if removed {
							if !os.IsNotExist(statErr) {
								t.Errorf("completed removal lost: path=%q err=%v", name, statErr)
							}
						} else if statErr != nil || !info.IsDir() {
							t.Errorf("unstarted/preview directory changed: path=%q err=%v", name, statErr)
						}
					}
				})
			}
		}
	}
}
