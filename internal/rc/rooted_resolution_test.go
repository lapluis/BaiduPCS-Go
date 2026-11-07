package rc

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsconfig"
)

func TestWindowsRootedEndpointUsesVolumeRoot(t *testing.T) {
	requireWindowsNames(t)
	root := t.TempDir()
	r := &Resolver{CWD: filepath.Join(root, "cwd", "nested")}
	for _, absolute := range []string{root, filepath.Join(root, "target.bin"), filepath.VolumeName(root) + `\`} {
		rooted := strings.TrimPrefix(absolute, filepath.VolumeName(absolute))
		for _, p := range []string{rooted, filepath.ToSlash(rooted)} {
			t.Run(p, func(t *testing.T) {
				e, err := r.Resolve(p)
				if err != nil || e.Root != absolute {
					t.Fatalf("resolved=%q err=%v want=%q", e.Root, err, absolute)
				}
			})
		}
	}
}

func TestWindowsLocalResolutionForms(t *testing.T) {
	requireWindowsNames(t)
	for _, tc := range []struct{ cwd, p, want string }{
		{`D:\base\nested`, `\file`, `D:\file`},
		{`D:\base\nested`, `/file`, `D:\file`},
		{`D:\base\nested`, `..\file`, `D:\base\file`},
		{`D:\base`, `C:\file`, `C:\file`},
		{`D:\base`, `\\host\share\file`, `\\host\share\file`},
		{`\\host\share\base`, `\file`, `\\host\share\file`},
		{`\\?\C:\base`, `\file`, `\\?\C:\file`},
		{`\\?\UNC\host\share\base`, `/file`, `\\?\UNC\host\share\file`},
	} {
		t.Run(tc.cwd+"/"+tc.p, func(t *testing.T) {
			got, err := ResolveLocalPath(tc.cwd, tc.p)
			if err != nil || got != tc.want {
				t.Fatal(got, err, tc.want)
			}
		})
	}
	for _, p := range []string{`C:file`, `D:`, `C:..\file`, ``} {
		if _, err := ResolveLocalPath(`D:\base`, p); err == nil {
			t.Fatal("ambiguous input accepted", p)
		}
	}
	if _, err := ResolveLocalPath("relative", `\file`); err == nil {
		t.Fatal("missing working volume silently accepted")
	}
}

func TestWindowsRootedLegacyFilterAndLog(t *testing.T) {
	requireWindowsNames(t)
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
	oldDir := pcsconfig.GetConfigDir()
	pcsconfig.SetConfigDir(filepath.Join(root, "config"))
	defer pcsconfig.SetConfigDir(oldDir)
	rooted := func(p string) string { return strings.TrimPrefix(p, filepath.VolumeName(p)) }
	log := filepath.Join(root, "events.log")
	var out, stderr bytes.Buffer
	code := runWithIO([]string{"lsf", rooted(data), "--files-from", rooted(selection), "--log-file", rooted(log)}, cwd, &out, &stderr)
	if code != 0 || strings.TrimSpace(out.String()) != "keep.txt" {
		t.Fatalf("code=%d out=%q err=%q", code, out.String(), stderr.String())
	}
	if _, err := os.Stat(log); err != nil {
		t.Fatal("log opened at wrong location", err)
	}
	if err := ValidateLogPath(context.Background(), cwd, rooted(selection), selection); err == nil {
		t.Fatal("rooted log bypassed input protection")
	}
}

func TestWindowsRootedDeleteDoesNotTouchNestedDecoy(t *testing.T) {
	requireWindowsNames(t)
	root := t.TempDir()
	cwd := filepath.Join(root, "cwd")
	intended := filepath.Join(root, "intended.txt")
	rooted := strings.TrimPrefix(intended, filepath.VolumeName(intended))
	decoy := filepath.Join(cwd, rooted)
	if err := os.MkdirAll(filepath.Dir(decoy), 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{intended, decoy} {
		if err := os.WriteFile(p, []byte("retain-or-delete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p := parsed{Args: []string{rooted}, Options: opts()}
	if err := runCommand(context.Background(), "deletefile", p, &Resolver{CWD: cwd}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(intended); !os.IsNotExist(err) {
		t.Fatal("rooted input was not deleted", err)
	}
	if b, err := os.ReadFile(decoy); err != nil || string(b) != "retain-or-delete" {
		t.Fatal("wrong nested file changed", err)
	}
}

func TestWindowsRootedLogUsesVolumeRoot(t *testing.T) {
	requireWindowsNames(t)
	root := t.TempDir()
	rooted := strings.TrimPrefix(filepath.Join(root, "events.log"), filepath.VolumeName(root))
	got, err := localLogPath(filepath.Join(root, "cwd"), rooted)
	if err != nil || got != filepath.Join(root, "events.log") {
		t.Fatal(got, err)
	}
}
