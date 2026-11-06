//go:build !windows

package filelock

import (
	"os"
	"syscall"
)

func tryLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, &os.PathError{Op: "lock", Path: path, Err: err}
	}
	return f, nil
}
