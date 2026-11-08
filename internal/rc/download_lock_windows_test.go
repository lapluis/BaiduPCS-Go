package rc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLongDownloadLeaseWindows(t *testing.T) {
	parent := t.TempDir()
	for len(utf16.Encode([]rune(parent))) < 300 {
		parent = filepath.Join(parent, strings.Repeat("segment", 7))
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(parent, "download.lock")
	if err := os.WriteFile(name, []byte("retained lock content"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := lockDownload(name)
	if err != nil {
		t.Fatalf("cannot lock an existing long-path file: %v", err)
	}
	defer lease.Close()
	if duplicate, err := lockDownload(name); err == nil {
		duplicate.Close()
		t.Fatal("long-path lease was not exclusive")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := lockDownload(name)
	if err != nil {
		t.Fatalf("released long-path lease cannot be acquired: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != "retained lock content" {
		t.Fatalf("locking changed file content: %q %v", data, err)
	}
}
