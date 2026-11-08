package rc

import (
	"path/filepath"
	"runtime"
	"strings"
)

// Older filepath versions treat only \\?\UNC as the volume, allowing Clean
// and Join to walk above the share. Use ordinary UNC syntax for lexical work,
// then restore the extended prefix before returning any filesystem pathname.
func ordinaryUNCPath(p string) (string, bool) {
	if runtime.GOOS != "windows" || len(p) < 8 || !strings.EqualFold(filepath.ToSlash(p[:8]), "//?/UNC/") {
		return "", false
	}
	return `\\` + p[8:], true
}

func restoreUNCPath(prefix, p string) string {
	return filepath.FromSlash(prefix) + strings.TrimPrefix(p, `\\`)
}

func localVolumeName(p string) string {
	if ordinary, ok := ordinaryUNCPath(p); ok {
		return restoreUNCPath(p[:8], filepath.VolumeName(ordinary))
	}
	return filepath.VolumeName(p)
}

func localCleanPath(p string) string {
	if ordinary, ok := ordinaryUNCPath(p); ok {
		return restoreUNCPath(p[:8], filepath.Clean(ordinary))
	}
	return filepath.Clean(p)
}

func localJoinPath(base, p string) string {
	if ordinary, ok := ordinaryUNCPath(base); ok {
		return restoreUNCPath(base[:8], filepath.Join(ordinary, p))
	}
	return filepath.Join(base, p)
}

func localDir(p string) string {
	if ordinary, ok := ordinaryUNCPath(p); ok {
		return restoreUNCPath(p[:8], filepath.Dir(ordinary))
	}
	return filepath.Dir(p)
}
