package pcsupdate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBatchUpdateRollback(t *testing.T) {
	for _, failRollback := range []bool{false, true} {
		dir := t.TempDir()
		var entries []stagedUpdate
		for _, name := range []string{"one", "two"} {
			target := filepath.Join(dir, name)
			staged := target + ".new"
			if err := os.WriteFile(target, []byte("original"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(staged, []byte("new"), 0700); err != nil {
				t.Fatal(err)
			}
			entries = append(entries, stagedUpdate{target, staged, target + ".backup"})
		}
		cleanup, err := commitUpdates(entries, func(from, to string) error {
			if from == entries[1].staged || (failRollback && from == entries[0].backup) {
				return errors.New("injected rename failure")
			}
			return os.Rename(from, to)
		})
		if err == nil {
			t.Fatal("failed install reported success")
		}
		if failRollback {
			if cleanup {
				t.Fatal("rollback failure permitted deleting backups")
			}
			data, err := os.ReadFile(entries[0].backup)
			if err != nil || string(data) != "original" {
				t.Fatal("original backup lost")
			}
		} else {
			if !cleanup {
				t.Fatal("successful rollback disallowed cleanup")
			}
			for _, entry := range entries {
				data, err := os.ReadFile(entry.target)
				if err != nil || string(data) != "original" {
					t.Fatal("original not restored")
				}
			}
		}
	}
}
