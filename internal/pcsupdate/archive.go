package pcsupdate

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func installArchive(reader *zip.Reader, directory, executable string) error {
	type entry struct {
		file   *zip.File
		target string
	}
	var entries []entry
	seen := map[string]bool{}
	for _, file := range reader.File {
		if file == nil {
			continue
		}
		name := strings.TrimSuffix(file.Name, "/")
		if name == "" || strings.ContainsAny(name, "\\:") || path.IsAbs(name) {
			return fmt.Errorf("invalid update path: %q", file.Name)
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".." || component == "." || component == "" {
				return fmt.Errorf("invalid update path: %q", file.Name)
			}
		}
		if file.FileInfo().IsDir() {
			continue
		}
		if !file.Mode().IsRegular() {
			return fmt.Errorf("non-regular update entry: %q", file.Name)
		}
		if _, rest, ok := strings.Cut(name, "/"); ok {
			name = rest
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if name == "BaiduPCS-Go" || name == "BaiduPCS-Go.exe" {
			target = executable
		}
		// Reject existing symbolic links in every component, including the target.
		for current := target; ; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symbolic link in update path: %s", current)
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
		key := strings.ToLower(filepath.Clean(target))
		if seen[key] {
			return fmt.Errorf("duplicate update target: %s", target)
		}
		seen[key] = true
		entries = append(entries, entry{file, target})
	}
	if len(entries) == 0 {
		return fmt.Errorf("update archive contains no files")
	}
	staging, err := os.MkdirTemp(directory, ".pcs-archive-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	// Read every entry to EOF (including ZIP checksum validation) before any
	// installed file is replaced. Keep large update contents on disk.
	for i, entry := range entries {
		source, err := entry.file.Open()
		if err != nil {
			return err
		}
		staged, err := os.Create(filepath.Join(staging, fmt.Sprint(i)))
		if err != nil {
			source.Close()
			return err
		}
		_, err = io.Copy(staged, source)
		closeErr := source.Close()
		stagedCloseErr := staged.Close()
		if err != nil {
			return fmt.Errorf("update %s: %w", entry.file.Name, err)
		}
		if closeErr != nil {
			return closeErr
		}
		if stagedCloseErr != nil {
			return stagedCloseErr
		}
	}
	for i, entry := range entries {
		source, err := os.Open(filepath.Join(staging, fmt.Sprint(i)))
		if err != nil {
			return err
		}
		err = update(entry.target, source)
		closeErr := source.Close()
		if err != nil {
			return fmt.Errorf("update %s: %w", entry.file.Name, err)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
