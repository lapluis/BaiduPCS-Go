package pcsupdate

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestBatchUpdateRetainsEveryRollbackFailure(t *testing.T) {
	for _, mask := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(mask), func(t *testing.T) {
			dir := t.TempDir()
			var entries []stagedUpdate
			for _, name := range []string{"one", "two", "three"} {
				target := filepath.Join(dir, name)
				if err := os.WriteFile(target, []byte("original "+name), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target+".new", []byte("new "+name), 0700); err != nil {
					t.Fatal(err)
				}
				entries = append(entries, stagedUpdate{target, target + ".new", target + ".backup"})
			}
			installErr := errors.New("synthetic installation failure")
			restoreErrs := []error{errors.New("first restore failure"), errors.New("second restore failure")}
			attempts := 0
			cleanup, err := commitUpdates(entries, func(from, to string) error {
				if from == entries[2].staged {
					return installErr
				}
				for i, entry := range entries {
					if from == entry.backup {
						attempts++
						if i < 2 && mask&(1<<i) != 0 {
							return restoreErrs[i]
						}
					}
				}
				return os.Rename(from, to)
			})
			if cleanup || attempts != 3 || !errors.Is(err, installErr) {
				t.Errorf("installation cause or recovery state lost: cleanup=%t attempts=%d err=%v", cleanup, attempts, err)
			}
			for i, entry := range entries {
				failed := i < 2 && mask&(1<<i) != 0
				name := entry.target
				if failed {
					name = entry.backup
					if !errors.Is(err, restoreErrs[i]) || !strings.Contains(err.Error(), entry.target) {
						t.Errorf("restore failure %d lost: %v", i, err)
					}
				}
				if data, readErr := os.ReadFile(name); readErr != nil || string(data) != "original "+filepath.Base(entry.target) {
					t.Errorf("original unavailable: %s data=%q err=%v", name, data, readErr)
				}
			}
		})
	}
}
