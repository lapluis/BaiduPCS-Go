//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package rc

import "os"

func fileHasMultipleLinks(string, os.FileInfo) (bool, error) { return true, nil }
