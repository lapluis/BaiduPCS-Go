package pcsclone

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsRootedPathsIncludeFilterAndLog(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rooted paths")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	data := filepath.Join(root, "data")
	for _, dir := range []string{cwd, data} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"keep.txt", "skip.txt"} {
		if err := os.WriteFile(filepath.Join(data, file), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	selection := filepath.Join(root, "selection.txt")
	if err := os.WriteFile(selection, []byte("keep.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rooted := func(p string) string { return strings.TrimPrefix(p, filepath.VolumeName(p)) }
	var listing, listingErr bytes.Buffer
	listingApp := &App{CWD: cwd, Version: "test", Stdout: &listing, Stderr: &listingErr}
	if code := listingApp.run([]string{"--config", filepath.Join(root, "config", "pcs.json"), "lsf", rooted(data), "--files-from", rooted(selection)}); code != 0 || strings.TrimSpace(listing.String()) != "keep.txt" {
		t.Fatal("rooted listing/filter failed", code, listing.String(), listingErr.String())
	}
	for _, protect := range []bool{false, true} {
		log := filepath.Join(root, "events.log")
		if protect {
			log = selection
		}
		var out, stderr bytes.Buffer
		app := &App{CWD: cwd, Version: "test", Stdout: &out, Stderr: &stderr}
		dst := filepath.Join(root, "destination")
		code := app.run([]string{"--config", filepath.Join(root, "config", "pcs.json"), "copy", rooted(data), rooted(dst), "--files-from", rooted(selection), "--log-file", rooted(log)})
		if protect {
			if code == 0 || !strings.Contains(stderr.String(), "log file overlaps") {
				t.Fatal("rooted log bypassed selection protection", code, stderr.String())
			}
		} else {
			if code != 0 {
				t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
			}
			if _, err := os.Stat(log); err != nil {
				t.Fatal("wrong log location", err)
			}
			if b, err := os.ReadFile(filepath.Join(dst, "keep.txt")); err != nil || string(b) != "fixture" {
				t.Fatal("selected file not copied", err)
			}
			if _, err := os.Stat(filepath.Join(dst, "skip.txt")); !os.IsNotExist(err) {
				t.Fatal("unselected file copied", err)
			}
		}
		if b, err := os.ReadFile(selection); err != nil || string(b) != "keep.txt\n" {
			t.Fatal("selection modified", err)
		}
	}
}
