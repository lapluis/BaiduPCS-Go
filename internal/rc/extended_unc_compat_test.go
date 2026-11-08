package rc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type uncRootProbe struct {
	Local
	stats int
}

func (p *uncRootProbe) Stat(context.Context, string) (Entry, error) {
	p.stats++
	return Entry{}, errors.New("fixture: filesystem must not be queried")
}

func TestExtendedUNCResolutionPreservesShareBoundary(t *testing.T) {
	requireWindowsNames(t)
	for _, tc := range []struct{ cwd, input, want string }{
		{`\\?\UNC\host\share\base`, `\file`, `\\?\UNC\host\share\file`},
		{`\\?\UNC\host\share\base`, `\..\file`, `\\?\UNC\host\share\file`},
		{`\\?\UNC\host\share\base`, `..\..\file`, `\\?\UNC\host\share\file`},
		{`\\?\UNC\host\share`, `..\file`, `\\?\UNC\host\share\file`},
		{`D:\base`, `\\?\UNC\host\share\..\file`, `\\?\UNC\host\share\file`},
		{`//?/unc/host/share/base`, `/file`, `\\?\unc\host\share\file`},
	} {
		t.Run(tc.input+"/"+tc.cwd, func(t *testing.T) {
			got, err := ResolveLocalPath(tc.cwd, tc.input)
			if err != nil || got != tc.want {
				t.Fatalf("ResolveLocalPath(%q,%q)=%q, %v; want %q", tc.cwd, tc.input, got, err, tc.want)
			}
		})
	}
}

func TestPurgeRejectsExtendedUNCShareRootBeforeFilesystemAccess(t *testing.T) {
	requireWindowsNames(t)
	for _, root := range []string{`\\?\UNC\host\share`, `\\?\UNC\host\share\`, `\\?\unc\host\share`} {
		for _, preview := range []bool{false, true} {
			probe := &uncRootProbe{}
			err := Delete(context.Background(), Endpoint{FS: probe, Root: root}, "purge", Options{DryRun: preview, MaxDelete: -1})
			if err == nil || !strings.Contains(err.Error(), "filesystem root") || probe.stats != 0 {
				t.Fatalf("share root reached filesystem: root=%q preview=%v calls=%d err=%v", root, preview, probe.stats, err)
			}
		}
	}
}

func TestExtendedUNCValidationSeparatesVolumeFromFilenames(t *testing.T) {
	requireWindowsNames(t)
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{`\\?\UNC\aux\con\file.txt`, true},
		{`\\?\UNC\host\share\AUX.txt`, false},
		{`\\?\UNC\host\share\trailing.`, false},
	} {
		if err := validLocalPath(tc.path); (err == nil) != tc.valid {
			t.Fatalf("validLocalPath(%q)=%v; valid=%v", tc.path, err, tc.valid)
		}
	}
}
