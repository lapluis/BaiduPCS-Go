package pcsclone

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

type statHashChangeContext struct {
	context.Context
	calls  int
	at     int
	change func()
}

func (c *statHashChangeContext) Err() error {
	c.calls++
	at := c.at
	if at == 0 {
		at = 2
	}
	if c.calls == at {
		c.change()
	}
	return c.Context.Err()
}

func TestStatHashRejectsSameMetadataReplacementAfterHash(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "file")
	if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, before) {
		t.Fatal("cannot capture original file identity")
	}
	changed := false
	// Initial metadata, hash metadata, two reads, hash final metadata, then
	// the command's final metadata query. The hash file handle is closed here.
	ctx := &statHashChangeContext{Context: context.Background(), at: 6, change: func() {
		if err := os.Rename(name, name+".original"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("replaced"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(name, before.ModTime(), before.ModTime()); err != nil {
			t.Fatal(err)
		}
		after, err := os.Stat(name)
		if err != nil || os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("replacement fixture failed: %v", err)
		}
		changed = true
	}}
	command := findCommand(topLevelCommands(), "stat")
	args, flags, err := parseCommandFlags([]string{name, "--hash"}, command.Flags)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	app := &App{CWD: root, Stdout: &output, Stderr: &bytes.Buffer{}, Resolver: &rc.Resolver{CWD: root}}
	err = command.Run(&commandContext{app: app, ctx: ctx, cmd: command, args: args, flags: flags})
	if !changed {
		t.Fatalf("fixture did not reach final metadata boundary: calls=%d", ctx.calls)
	}
	if err == nil || !strings.Contains(err.Error(), "changed") || output.Len() != 0 {
		t.Errorf("published hash for replaced file: output=%q err=%v", output.String(), err)
	}
	for _, item := range []struct{ name, content string }{{name, "replaced"}, {name + ".original", "original"}} {
		if data, err := os.ReadFile(item.name); err != nil || string(data) != item.content {
			t.Errorf("stat changed file: data=%q err=%v", data, err)
		}
	}
}

func TestStatHashRejectsMetadataChangeBeforeHash(t *testing.T) {
	for _, mode := range []string{"size", "mtime", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			name := filepath.Join(root, "file")
			if err := os.WriteFile(name, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			ctx := &statHashChangeContext{Context: context.Background(), change: func() {
				if mode == "size" {
					if err := os.WriteFile(name, []byte("new contents"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if mode == "mtime" {
					future := before.ModTime().Add(time.Hour)
					if err := os.Chtimes(name, future, future); err != nil {
						t.Fatal(err)
					}
				}
			}}
			command := findCommand(topLevelCommands(), "stat")
			args, flags, err := parseCommandFlags([]string{name, "--hash"}, command.Flags)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			app := &App{CWD: root, Stdout: &output, Stderr: &bytes.Buffer{}, Resolver: &rc.Resolver{CWD: root}}
			err = command.Run(&commandContext{app: app, ctx: ctx, cmd: command, args: args, flags: flags})
			if mode == "healthy" {
				var result statResult
				if err != nil || json.Unmarshal(output.Bytes(), &result) != nil || result.Size != 8 || result.Hash != "919c8b643b7133116b02fc0d9bb7df3f" {
					t.Fatalf("stable stat hash failed: output=%q err=%v", output.String(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "changed") || output.Len() != 0 {
				t.Fatalf("inconsistent stat hash published: output=%q err=%v", output.String(), err)
			}
		})
	}
}
