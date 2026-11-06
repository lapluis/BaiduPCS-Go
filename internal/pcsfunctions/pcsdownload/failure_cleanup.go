package pcsdownload

import (
	"errors"
	"os"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/filelock"
)

// Use exclusive creation to distinguish our empty artifact from an existing
// user file. The fallback must not create a file removed by another process.
func openDownloadOutput(path string) (*os.File, bool, error) {
	// Resolve final symlinks (including dangling ones) so exclusive creation
	// tracks the actual output without removing the user's link on failure.
	original := path
	path, err := filelock.CanonicalPath(path)
	if err != nil {
		return nil, false, &os.PathError{Op: "open", Path: original, Err: err}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0666)
	if err == nil {
		return file, true, nil
	}
	if !os.IsExist(err) {
		return nil, false, err
	}
	file, err = os.OpenFile(path, os.O_WRONLY, 0666)
	return file, false, err
}

func removeOwnedEmptyDownload(file *os.File, path, checkpoint string) (err error) {
	// A later downloader may have acquired ownership after Execute returned.
	// Keep its checkpoint lease through identity checks, close and removal.
	lease, err := filelock.TryLock(checkpoint + ".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	owned, err := file.Stat()
	if err != nil {
		return err
	}
	if !owned.Mode().IsRegular() || owned.Size() != 0 {
		return nil
	}
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(owned, current) || current.Size() != 0 {
		return nil
	}
	// Windows requires the output handle to be closed before unlinking it.
	if err := file.Close(); err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
