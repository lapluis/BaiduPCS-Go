package pcsupdate

import (
	"fmt"
)

type stagedUpdate struct{ target, staged, backup string }

// commitUpdates keeps originals until the entire batch has been installed.
// The boolean permits cleanup only when originals are installed or restored.
func commitUpdates(entries []stagedUpdate, rename func(string, string) error) (bool, error) {
	moved := 0
	rollback := func(cause error) (bool, error) {
		var restoreErr error
		for i := moved - 1; i >= 0; i-- {
			if err := rename(entries[i].backup, entries[i].target); err != nil {
				restoreErr = fmt.Errorf("restore %s: %w", entries[i].target, err)
			}
		}
		if restoreErr != nil {
			return false, fmt.Errorf("install failed: %v; rollback failed: %w; backups retained", cause, restoreErr)
		}
		return true, cause
	}
	for _, entry := range entries {
		if err := rename(entry.target, entry.backup); err != nil {
			return rollback(err)
		}
		moved++
		if err := rename(entry.staged, entry.target); err != nil {
			return rollback(err)
		}
	}
	return true, nil
}
