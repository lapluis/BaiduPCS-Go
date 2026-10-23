package pcsupdate

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func update(targetPath string, src io.Reader) error {
	info, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("update target is not a regular file: %s", targetPath)
	}
	oldPath := filepath.Join(filepath.Dir(targetPath), "old"+filepath.Base(targetPath))
	if _, err := os.Lstat(oldPath); !os.IsNotExist(err) {
		if err != nil {
			return err
		}
		return fmt.Errorf("update backup already exists: %s", oldPath)
	}
	staged, err := os.CreateTemp(filepath.Dir(targetPath), ".pcs-update-")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	if err = staged.Chmod(info.Mode().Perm()); err == nil {
		_, err = io.Copy(staged, src)
	}
	if err == nil {
		err = staged.Sync()
	}
	closeErr := staged.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(targetPath, oldPath); err != nil {
		return err
	}
	if err = os.Rename(staged.Name(), targetPath); err != nil {
		if rollbackErr := os.Rename(oldPath, targetPath); rollbackErr != nil {
			return fmt.Errorf("install update: %v; restore original: %w", err, rollbackErr)
		}
		return err
	}
	return os.Remove(oldPath)
}
