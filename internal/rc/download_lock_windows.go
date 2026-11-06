//go:build windows

package rc

import (
	"golang.org/x/sys/windows"
	"os"
)

// A persistent lock file needs no stale-PID recovery: Windows releases the
// exclusive handle even when the parent process is terminated.
func lockDownload(name string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), name), nil
}
