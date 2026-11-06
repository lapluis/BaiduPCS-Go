package pcsconfig

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type configTempFile interface {
	io.WriteCloser
	Name() string
	Sync() error
	Chmod(os.FileMode) error
}

var createConfigTemp = func(dir string) (configTempFile, error) {
	return os.CreateTemp(dir, ".pcs-config-*")
}

var writeConfigData = func(w io.Writer, data []byte) (int, error) { return w.Write(data) }
var replaceConfigFile = os.Rename

// saveConfigData never changes the previous file until a complete snapshot has
// been written, synced and closed. No configuration handle survives an I/O call.
func saveConfigData(path string, data []byte) error {
	if path == "" {
		return ErrConfigFileNotExist
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// Replace the target of a symbolic-link configuration, including a link
	// whose target has not been initialized yet. Keep the link itself intact.
	var err error
	path, err = configDestination(path)
	if err != nil {
		return err
	}
	mode := os.FileMode(0600)
	// Retain the old requirement that an existing configuration is writable,
	// rather than bypassing file permissions through directory replacement.
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err == nil {
		info, statErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(statErr, closeErr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("configuration is not a regular file: %s", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		if os.IsPermission(err) {
			return ErrConfigFileNoPermission
		}
		return err
	}
	temp, err := createConfigTemp(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	err = temp.Chmod(mode)
	if err == nil {
		var n int
		n, err = writeConfigData(temp, data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		return err
	}
	return replaceConfigFile(temp.Name(), path)
}

func configDestination(path string) (string, error) {
	for links := 0; ; links++ {
		if links >= 255 {
			return "", errors.New("too many symbolic links in configuration path")
		}
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if info == nil || info.Mode()&os.ModeSymlink == 0 {
			break
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}
