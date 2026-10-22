package pcsutil_test

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil"
	"os"
	"path/filepath"
	"testing"
)

func TestWalkDir(t *testing.T) {
	d := t.TempDir()
	os.MkdirAll(filepath.Join(d, "nested"), 0755)
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("a"), 0600)
	os.WriteFile(filepath.Join(d, "nested", "b.txt"), []byte("b"), 0600)
	files, err := pcsutil.WalkDir(d, ".txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatal(files)
	}
}
