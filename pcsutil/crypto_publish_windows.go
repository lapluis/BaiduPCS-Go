package pcsutil

import "golang.org/x/sys/windows"

func publishCryptoFile(stage, target string) error {
	from, err := windows.UTF16PtrFromString(stage)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

// The move above requests write-through without MOVEFILE_REPLACE_EXISTING.
func syncCryptoPublication(string) error { return nil }
