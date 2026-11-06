package downloader

import (
	"errors"
	"fmt"
	"os"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/filelock"
)

var ErrCheckpointConflict = errors.New("download checkpoint ownership changed")

// discardRanges invalidates an earlier ranged attempt before a single stream
// can overwrite its completed prefix. A failed stream must restart from zero.
func (is *InstanceState) discardRanges() (err error) {
	is.mu.Lock()
	defer is.mu.Unlock()
	defer func() {
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrCheckpointSave, err)
		}
	}()
	if is.saveFile == nil {
		return nil
	}
	if err := is.verifyCheckpointLocked(); err != nil {
		return err
	}
	if err := is.saveFile.Truncate(0); err != nil {
		return err
	}
	return is.saveFile.Sync()
}

func (is *InstanceState) claimCheckpoint() {
	if _, err := is.saveFile.Stat(); err != nil {
		is.initErr = err
		is.saveFile.Close()
		return
	}
	path, err := filelock.CanonicalPath(is.saveFile.Name())
	if err == nil {
		is.filename = path
		is.lease, err = filelock.TryLock(path + ".lock")
	}
	if err == nil {
		err = is.verifyCheckpointLocked()
	}
	if err != nil {
		is.initErr = fmt.Errorf("claim download checkpoint: %w", err)
		is.saveFile.Close()
		if is.lease != nil {
			is.lease.Close()
			is.lease = nil
		}
	}
}

// Managed downloads acquire ownership before opening or creating state, so an
// unsuccessful contender cannot create an empty record during owner cleanup.
func openCheckpointState(path string, format InstanceStateStorageFormat) (*InstanceState, error) {
	path, err := filelock.CanonicalPath(path)
	if err != nil {
		return nil, err
	}
	lease, err := filelock.TryLock(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("claim download checkpoint: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		lease.Close()
		return nil, err
	}
	state := &InstanceState{saveFile: file, filename: path, lease: lease, format: format}
	if err := state.verifyCheckpointLocked(); err != nil {
		state.Close()
		return nil, err
	}
	return state, nil
}

func (is *InstanceState) verifyCheckpointLocked() error {
	if is.initErr != nil {
		return is.initErr
	}
	if is.closed {
		return os.ErrClosed
	}
	if is.saveFile == nil {
		return nil
	}
	owned, err := is.saveFile.Stat()
	if err != nil {
		return err
	}
	current, err := os.Stat(is.filename)
	if err != nil {
		return err
	}
	if !os.SameFile(owned, current) {
		return ErrCheckpointConflict
	}
	return nil
}

func (is *InstanceState) closeCheckpointLocked() error {
	if is.closed {
		return nil
	}
	is.closed = true
	var err error
	if is.saveFile != nil {
		err = is.saveFile.Close()
		if errors.Is(err, os.ErrClosed) {
			err = nil
		}
	}
	if is.lease != nil {
		err = errors.Join(err, is.lease.Close())
		is.lease = nil
	}
	return err
}

func (is *InstanceState) removeCheckpoint() (err error) {
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.closed {
		return os.ErrClosed
	}
	if is.initErr != nil {
		return is.initErr
	}
	defer func() { err = errors.Join(err, is.closeCheckpointLocked()) }()
	if is.saveFile == nil {
		return nil
	}
	if err := is.verifyCheckpointLocked(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := is.saveFile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
		return err
	}
	// Keep the independent lease until removal finishes. Closing the data
	// handle must not let a later owner create progress that we then delete.
	err = os.Remove(is.filename)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
