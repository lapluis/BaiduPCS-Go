package rc

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsExplicitDestinationComponentsRejected(t *testing.T) {
	requireWindowsNames(t)
	for _, target := range []string{"bad.", "bad /child", "parent./child", "COM¹/child", "CONIN$/child", "bad\x01dir/child"} {
		for _, mode := range []string{"copy", "sync", "move", "copyto", "moveto"} {
			t.Run(fmt.Sprintf("%s/%s", mode, target), func(t *testing.T) {
				root := t.TempDir()
				dst := Endpoint{FS: &Local{}, Root: filepath.Join(root, filepath.FromSlash(target))}
				src := Endpoint{FS: &windowsNameSource{entries: map[string]Entry{"file": {Size: 8}}}, Root: "/source"}
				var err error
				if mode == "copyto" || mode == "moveto" {
					src.Root = "/source/file"
					dst.Root = filepath.Join(dst.Root, "out.bin")
					_, err = FilePlan(context.Background(), src, dst, mode == "moveto", opts())
				} else {
					_, err = BuildPlan(context.Background(), src, dst, mode, opts())
				}
				if err == nil || !strings.Contains(err.Error(), "Windows filename") {
					t.Fatal("target components not rejected by path validation", err)
				}
				entries, err := os.ReadDir(root)
				if err != nil || len(entries) != 0 {
					t.Fatal("planning modified destination", entries, err)
				}
			})
		}
	}
}

func TestWindowsMkdirRejectsAliasedComponents(t *testing.T) {
	requireWindowsNames(t)
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			root := t.TempDir()
			r := &Resolver{CWD: root}
			p := parsed{Args: []string{filepath.Join(root, "bad.", "child")}, Options: opts()}
			p.DryRun = dry
			err := runCommand(context.Background(), "mkdir", p, r, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "Windows filename") {
				t.Fatal("mkdir accepted aliased directory", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("mkdir changed destination", entries, err)
			}
		})
	}
}

func TestWindowsLocalPathPreservesVolumeSyntax(t *testing.T) {
	requireWindowsNames(t)
	for _, p := range []string{`C:\`, `C:\normal\字幕`, `C:\.hidden\file`, `\\server\share\`, `\\server\share\folder\file`, `\\?\C:\normal\file`, `\\?\UNC\server\share\folder`, `.`, `normal\..\safe`} {
		t.Run(p, func(t *testing.T) {
			if err := validLocalPath(p); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, p := range []string{`C:\bad.\child`, `\\server\share\bad \child`, `\\?\C:\CONIN$\child`, `\\?\UNC\server\share\COM¹\child`} {
		t.Run(p, func(t *testing.T) {
			if err := validLocalPath(p); err == nil {
				t.Fatal("invalid component accepted")
			}
		})
	}
}

func TestWindowsLocalMkdirRejectsBeforeCreatingParents(t *testing.T) {
	requireWindowsNames(t)
	root := t.TempDir()
	err := (&Local{}).Mkdir(context.Background(), filepath.Join(root, "new", "bad.", "child"))
	if err == nil || !strings.Contains(err.Error(), "Windows filename") {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("invalid mkdir partially created parents", entries, err)
	}
}
