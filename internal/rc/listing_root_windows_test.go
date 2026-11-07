package rc

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLocalListingRejectsRootJunction(t *testing.T) {
	link, _ := newListingRootJunction(t)
	assertListingRejectsLinkedRoot(t, link)
}

func newListingRootJunction(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "junction")
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		"New-Item -ItemType Junction -Path $env:PCS_LIST_ROOT_JUNCTION -Target $env:PCS_LIST_ROOT_TARGET -ErrorAction Stop | Out-Null")
	cmd.Env = append(os.Environ(), "PCS_LIST_ROOT_JUNCTION="+link, "PCS_LIST_ROOT_TARGET="+target)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create fixture junction: %v %s", err, output)
	}
	return link, target
}

func TestJunctionAncestorCannotRedirectLocalOperations(t *testing.T) {
	for _, operation := range []string{"resolve", "mkdir", "copyto", "moveto", "deletefile"} {
		t.Run(operation, func(t *testing.T) {
			link, target := newListingRootJunction(t)
			source := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(source, []byte("source fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			viaLink := filepath.Join(link, "file")
			r := &Resolver{CWD: filepath.Dir(link)}
			var err error
			if operation == "resolve" {
				for _, name := range []string{viaLink, filepath.Join(link, "missing")} {
					if _, err := r.Resolve(name); err == nil {
						t.Errorf("resolver accepted junction ancestor: %s", name)
					}
				}
				return
			}
			args := []string{viaLink}
			if operation == "mkdir" {
				args = []string{filepath.Join(link, "created")}
			} else if operation == "copyto" || operation == "moveto" {
				args = []string{source, viaLink}
			}
			p, err := parse(args)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err = runCommand(context.Background(), operation, p, r, &output)
			if err == nil || output.Len() != 0 {
				t.Errorf("operation followed junction ancestor: output=%q err=%v", output.String(), err)
			}
			if data, err := os.ReadFile(filepath.Join(target, "file")); err != nil || string(data) != "fixture" {
				t.Errorf("physical target changed: data=%q err=%v", data, err)
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != "source fixture" {
				t.Errorf("source changed: data=%q err=%v", data, err)
			}
			if _, err := os.Stat(filepath.Join(target, "created")); !os.IsNotExist(err) {
				t.Errorf("directory created through junction: %v", err)
			}
		})
	}
}
