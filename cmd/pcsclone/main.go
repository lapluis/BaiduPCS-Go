// Command pcsclone is a standalone local/Baidu netdisk file-operation
// front-end. It shares the configuration loader, HTTP clients and transfer
// engine with BaiduPCS-Go but does not start the legacy interactive shell.
package main

import (
	"os"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsclone"
)

// Version is the pcsclone release string. Builds may override it with
// -ldflags "-X main.Version=<version>".
var Version = "v4.0.2-dev"

func main() {
	os.Exit(pcsclone.Run(os.Args[1:], Version, os.Stdout, os.Stderr))
}
