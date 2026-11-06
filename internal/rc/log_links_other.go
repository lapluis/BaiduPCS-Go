//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package rc

import "os"

func logHasMultipleLinks(string, os.FileInfo) (bool, error) { return true, nil }
