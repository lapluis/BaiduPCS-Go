package pcsutil

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type walkFaultEntry struct {
	os.DirEntry
	err  error
	link bool
}

func (e walkFaultEntry) Info() (os.FileInfo, error) {
	if e.err != nil {
		return nil, e.err
	}
	info, err := e.DirEntry.Info()
	if err == nil && e.link {
		info = walkLinkInfo{info}
	}
	return info, err
}

type walkLinkInfo struct{ os.FileInfo }

func (i walkLinkInfo) Mode() os.FileMode { return os.ModeSymlink | 0600 }

func TestWalkDirPreservesInspectionFailures(t *testing.T) {
	for _, stage := range []string{"entry-info", "link-target", "read-directory"} {
		for _, cause := range []error{os.ErrPermission, io.ErrUnexpectedEOF} {
			t.Run(stage+"/"+cause.Error(), func(t *testing.T) {
				root := t.TempDir()
				for _, name := range []string{"a.txt", "b.txt"} {
					if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				failedPath := filepath.Join(root, "b.txt")
				failure := &os.PathError{Op: "fixture-inspect", Path: failedPath, Err: cause}
				stat := func(name string) (os.FileInfo, error) {
					if stage != "link-target" || name != failedPath {
						t.Errorf("unexpected target inspection: %s", name)
					}
					return nil, failure
				}
				readDir := func(name string) ([]os.DirEntry, error) {
					if stage == "read-directory" {
						return nil, failure
					}
					entries, err := os.ReadDir(name)
					for i, entry := range entries {
						if entry.Name() == "b.txt" {
							wrapped := walkFaultEntry{DirEntry: entry, link: stage == "link-target"}
							if stage == "entry-info" {
								wrapped.err = failure
							}
							entries[i] = wrapped
						}
					}
					return entries, err
				}
				files, err := walkDir(root, ".txt", stat, readDir)
				if !errors.Is(err, cause) {
					t.Fatalf("inspection failure lost: files=%v err=%v", files, err)
				}
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || pathErr.Path != failedPath {
					t.Fatalf("failed path lost: %v", err)
				}
			})
		}
	}
}

func TestWalkDirMissingAndHealthyLinkTargets(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy", true: "missing"}[missing], func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, "file.txt")
			if err := os.WriteFile(name, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			stat := func(path string) (os.FileInfo, error) {
				calls++
				if path != name {
					t.Errorf("unexpected target path: %s", path)
				}
				if missing {
					return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrNotExist}
				}
				return os.Stat(path)
			}
			readDir := func(path string) ([]os.DirEntry, error) {
				entries, err := os.ReadDir(path)
				for i, entry := range entries {
					entries[i] = walkFaultEntry{DirEntry: entry, link: true}
				}
				return entries, err
			}
			files, err := walkDir(root, ".txt", stat, readDir)
			wantCount := 1
			if missing {
				wantCount = 0
			}
			if err != nil || calls != 1 || len(files) != wantCount || (wantCount == 1 && files[0] != name) {
				t.Fatalf("link target result: files=%v err=%v checks=%d", files, err, calls)
			}
		})
	}
}
