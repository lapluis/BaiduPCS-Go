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

// SaveConfigData publishes a serialized configuration snapshot using the same
// permission checks, sync and atomic replacement as account configuration saves.
// Callers are responsible for validating the destination's storage role.
func SaveConfigData(path string, data []byte) error {
	return saveConfigData(path, data)
}

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

// ResolveConfigDestination resolves an existing or future configuration target,
// following existing symbolic links even when their targets or parents are
// missing. It does not create directories or files.
func ResolveConfigDestination(path string) (string, error) {
	resolved, err := configDestination(path)
	if err == nil || !os.IsNotExist(err) {
		return resolved, err
	}
	return futureConfigDestination(path, 0)
}

func futureConfigDestination(path string, links int) (string, error) {
	if links >= 255 {
		return "", errors.New("too many symbolic links in configuration path")
	}
	var missing []string
	for {
		info, err := os.Lstat(path)
		if err == nil {
			var resolved string
			if info.Mode()&os.ModeSymlink != 0 {
				target, readErr := configLinkTarget(path)
				if readErr != nil {
					return "", readErr
				}
				resolved, err = futureConfigDestination(target, links+1)
			} else {
				resolved, err = filepath.EvalSymlinks(path)
			}
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent, name := configPathParts(path)
		if parent == path {
			return "", err
		}
		missing = append(missing, name)
		path = parent
	}
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
		target, err := configLinkTarget(path)
		if err != nil {
			return "", err
		}
		path = target
	}
	dir, name := configPathParts(path)
	parent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, name), nil
}

func configLinkTarget(path string) (string, error) {
	target, err := os.Readlink(path)
	if err != nil || filepath.IsAbs(target) {
		return target, err
	}
	// Resolve the directory before cleaning a relative target containing ..:
	// its physical parent can differ from the logical invocation pathname.
	dir, _ := configPathParts(path)
	parent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	// Keep the target's internal components intact until EvalSymlinks has
	// interpreted them: sub/.. need not refer to parent when sub is a link.
	if len(target) > 0 && os.IsPathSeparator(target[0]) {
		return filepath.VolumeName(parent) + target, nil
	}
	if !os.IsPathSeparator(parent[len(parent)-1]) {
		parent += string(filepath.Separator)
	}
	return parent + target, nil
}

// filepath.Dir cleans .. before symbolic links can be resolved. Split keeps
// those components, while trimming only a trailing separator for upward walks.
func configPathParts(path string) (dir, name string) {
	dir, name = filepath.Split(path)
	if dir == "" {
		return ".", name
	}
	for len(dir) > len(filepath.VolumeName(dir))+1 && os.IsPathSeparator(dir[len(dir)-1]) {
		dir = dir[:len(dir)-1]
	}
	return dir, name
}
