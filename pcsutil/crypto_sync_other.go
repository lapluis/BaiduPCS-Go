//go:build !windows

package pcsutil

import (
	"errors"
	"os"
	"path/filepath"
)

func syncCryptoPublication(target string) error {
	dir, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
