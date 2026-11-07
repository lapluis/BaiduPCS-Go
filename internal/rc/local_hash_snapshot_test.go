package rc

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type localHashChangeContext struct {
	context.Context
	calls  int
	change func()
}

func (c *localHashChangeContext) Err() error {
	c.calls++
	// Local.Stat checks the context before capturing metadata. The next check
	// is the context reader's boundary before its first file read.
	if c.calls == 2 {
		c.change()
	}
	return c.Context.Err()
}

func TestLocalHashRejectsChangesDuringRead(t *testing.T) {
	for _, mode := range []string{"size", "mtime", "replacement", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "replacement" && runtime.GOOS == "windows" {
				t.Skip("ordinary Windows read handles prevent renaming an open file")
			}
			name := filepath.Join(t.TempDir(), "file")
			content := []byte("original")
			if err := os.WriteFile(name, content, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &localHashChangeContext{Context: context.Background(), change: func() {
				switch mode {
				case "size":
					if err := os.WriteFile(name, []byte("external modification"), 0600); err != nil {
						t.Fatal(err)
					}
				case "mtime":
					future := before.ModTime().Add(time.Hour)
					if err := os.Chtimes(name, future, future); err != nil {
						t.Fatal(err)
					}
				case "replacement":
					if err := os.Rename(name, name+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(name, []byte("replaced"), 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			}}
			got, err := (&Local{}).Hash(ctx, name)
			if ctx.calls < 2 {
				t.Fatal("fixture did not reach file read")
			}
			if mode == "unchanged" {
				digest := md5.Sum(content)
				if err != nil || got != hex.EncodeToString(digest[:]) {
					t.Fatalf("stable hash failed: digest=%q err=%v", got, err)
				}
			} else if err == nil || got != "" || !strings.Contains(err.Error(), "changed") {
				t.Fatalf("changed file produced digest: digest=%q err=%v", got, err)
			}
		})
	}
}
