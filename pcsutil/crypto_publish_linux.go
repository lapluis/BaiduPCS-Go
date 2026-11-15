package pcsutil

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func publishCryptoFile(stage, target string) error {
	err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return os.Link(stage, target)
	}
	return err
}
