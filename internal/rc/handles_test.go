package rc

import (
	"runtime"
	"testing"
)

// Handles must only claim new command names or explicit endpoint paths. Legacy
// commands with their original pan paths keep the existing CLI implementation.
func TestHandlesPreservesLegacyDispatch(t *testing.T) {
	if Handles(nil) {
		t.Fatal("empty arguments must use the legacy entry point")
	}
	for _, args := range [][]string{
		{"ls", "/Temp"},
		{"cd", "/Temp"},
		{"upload", "local.txt", "/Temp"},
		{"download", "/Temp/a"},
		{"cp", "/Temp/a", "/Temp/b"},
		{"mv", "/Temp/a", "/Temp/b"},
		{"rm", "/Temp/a"},
		{"mkdir", "/Temp/new"},
		{"tree", "/Temp"},
		{"quota"},
	} {
		if Handles(args) {
			t.Fatalf("legacy command intercepted: %v", args)
		}
	}
	for _, args := range [][]string{
		{"copy", "a", "b"},
		{"lsjson", "bd:/Temp"},
		{"rc", "mkdir", "./local"},
		{"__rc_transfer", "download", "1", "/Temp/a", "local", "0"},
		{"mkdir", "bd:/Temp/new"},
		{"tree", "bd:/Temp"},
		{"quota", "bd:"},
	} {
		if !Handles(args) {
			t.Fatalf("rc command not intercepted: %v", args)
		}
	}
	if runtime.GOOS == "windows" && !Handles([]string{"mkdir", `D:\Backup`}) {
		t.Fatal("explicit Windows path not intercepted")
	}
}
