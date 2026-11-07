//go:build !windows

package rc

import "strings"

func localPathsOverlap(a, b string) (bool, error) {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/"), nil
}

func validateSelectedLocalPaths(source, destination map[string]Entry, o Options) error {
	return nil
}
