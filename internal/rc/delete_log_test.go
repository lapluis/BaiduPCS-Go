package rc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type refusedRemoval struct {
	Backend
	err error
}

func (b *refusedRemoval) Remove(context.Context, string, bool) error { return b.err }

func TestSingleDeletionLogsOnlySuccessfulActions(t *testing.T) {
	for _, mode := range []string{"deletefile", "rmdir", "purge"} {
		for _, scenario := range []string{"success", "failure", "dry-run"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				p := filepath.Join(t.TempDir(), "item")
				if mode != "deletefile" {
					if err := os.Mkdir(p, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(p, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				failure := errors.New("fixture removal failure")
				var backend Backend = &Local{}
				if scenario == "failure" {
					backend = &refusedRemoval{Backend: backend, err: failure}
				}
				var events []string
				opts := Options{MaxDelete: -1, DryRun: scenario == "dry-run", Emit: func(kind, item string) { events = append(events, kind+":"+item) }}
				err := Delete(context.Background(), Endpoint{FS: backend, Root: p}, mode, opts)
				if scenario == "failure" {
					if !errors.Is(err, failure) || len(events) != 0 {
						t.Fatalf("failed removal logged success: err=%v events=%v", err, events)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					kind := "delete"
					if mode != "deletefile" {
						kind = "rmdir"
					}
					if scenario == "dry-run" {
						kind = "would-" + kind
					}
					if len(events) != 1 || events[0] != kind+":"+p {
						t.Fatalf("missing or duplicate event: %v", events)
					}
				}
				_, statErr := os.Stat(p)
				if scenario == "success" {
					if !os.IsNotExist(statErr) {
						t.Fatalf("removal result changed: %v", statErr)
					}
				} else if statErr != nil {
					t.Fatalf("failed/dry removal changed item: %v", statErr)
				}
			})
		}
	}
}
