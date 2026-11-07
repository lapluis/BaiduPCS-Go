//go:build !windows

package downloader

import "os"

func openReplaceableCheckpointFile(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
}
