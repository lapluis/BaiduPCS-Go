package pcsupdate

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type brokenUpdateReader struct{}

func (brokenUpdateReader) Read([]byte) (int, error) { return 0, errors.New("synthetic failure") }
func TestUpdatePreservesOriginalOnReadFailure(t *testing.T) {
	target := filepath.Join(t.TempDir(), "program")
	if err := os.WriteFile(target, []byte("original"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := update(target, io.MultiReader(strings.NewReader("partial"), brokenUpdateReader{})); err == nil {
		t.Fatal("read error ignored")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "original" {
		t.Fatal("original damaged")
	}
	entries, err := os.ReadDir(filepath.Dir(target))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary file leaked")
	}
	if err := update(target, strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "replacement" {
		t.Fatal("replacement missing")
	}
	backup := filepath.Join(filepath.Dir(target), "oldprogram")
	if err := os.WriteFile(backup, []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := update(target, strings.NewReader("next")); err == nil {
		t.Fatal("backup conflict ignored")
	}
	data, _ = os.ReadFile(backup)
	if string(data) != "backup" {
		t.Fatal("backup overwritten")
	}
}
func TestUpdateMissingTargetFails(t *testing.T) {
	if err := update(filepath.Join(t.TempDir(), "missing"), strings.NewReader("new")); err == nil {
		t.Fatal("missing target accepted")
	}
}
