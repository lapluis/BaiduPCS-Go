//go:build !windows && !linux

package pcsutil

import "os"

func publishCryptoFile(stage, target string) error { return os.Link(stage, target) }
