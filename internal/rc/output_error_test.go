package rc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingCommandOutput struct {
	err          error
	short        bool
	allow, calls int
}

func (w *failingCommandOutput) Write(p []byte) (int, error) {
	w.calls++
	if w.calls <= w.allow {
		return len(p), nil
	}
	if w.short {
		return len(p) / 2, nil
	}
	return 0, w.err
}

func TestLegacyQueryOutputFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{CWD: root, Aliases: map[string]uint64{"bd": 7, "zz": 8}}
	for _, cmd := range []string{"listremotes", "lsf", "lsd", "lsjson", "size", "tree"} {
		for _, short := range []bool{false, true} {
			name := cmd
			if short {
				name += "/short"
			}
			t.Run(name, func(t *testing.T) {
				args := []string{root}
				if cmd == "listremotes" {
					args = nil
				}
				p, err := parse(args)
				if err != nil {
					t.Fatal(err)
				}
				failure := errors.New("fixture command output failure")
				writer := &failingCommandOutput{err: failure, short: short}
				err = runCommand(context.Background(), cmd, p, r, writer)
				want := error(failure)
				if short {
					want = io.ErrShortWrite
				}
				if !errors.Is(err, want) {
					t.Fatalf("output failure lost: err=%v want=%v calls=%d", err, want, writer.calls)
				}
				if writer.calls != 1 {
					t.Fatalf("continued writing failed output: %d writes", writer.calls)
				}
			})
		}
	}
}

func TestLegacyHelpOutputFailureAndSuccess(t *testing.T) {
	for _, args := range [][]string{nil, {"rc"}, {"help"}, {"--help"}, {"rc", "help"}, {"lsf", "--help"}} {
		name := strings.Join(args, " ")
		if name == "" {
			name = "empty"
		}
		for _, short := range []bool{false, true} {
			caseName := name
			if short {
				caseName += "/short"
			}
			t.Run(caseName, func(t *testing.T) {
				failure := errors.New("fixture help output failure")
				writer := &failingCommandOutput{err: failure, short: short}
				var stderr bytes.Buffer
				code := runWithIO(args, t.TempDir(), writer, &stderr)
				want := failure.Error()
				if short {
					want = io.ErrShortWrite.Error()
				}
				if code != 1 || strings.Count(stderr.String(), want) != 1 || writer.calls != 1 {
					t.Fatalf("help output failure lost: code=%d writes=%d stderr=%q", code, writer.calls, stderr.String())
				}
			})
		}
		t.Run(name+"/normal", func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := runWithIO(args, t.TempDir(), &out, &errOut); code != 0 || out.String() != Help || errOut.Len() != 0 {
				t.Fatalf("normal help changed: code=%d out=%q err=%q", code, out.String(), errOut.String())
			}
		})
	}
}

func TestLegacyQueryOutputCompatibility(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "two.txt"), []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Resolver{CWD: root, Aliases: map[string]uint64{"zz": 8, "bd": 7}}
	for _, cmd := range []string{"listremotes", "lsf", "lsd", "lsjson", "size", "tree"} {
		t.Run(cmd, func(t *testing.T) {
			args := []string{root}
			if cmd == "listremotes" {
				args = nil
			}
			p, err := parse(args)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if err := runCommand(context.Background(), cmd, p, r, &out); err != nil {
				t.Fatal(err)
			}
			switch cmd {
			case "lsjson":
				var entries []Entry
				if err := json.Unmarshal(out.Bytes(), &entries); err != nil || len(entries) != 2 || entries[0].Path != "one.txt" || entries[0].Size != 3 || !entries[1].IsDir {
					t.Fatalf("JSON listing changed: %q (%v)", out.String(), err)
				}
			case "size":
				var result map[string]int64
				if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["count"] != 2 || result["bytes"] != 8 {
					t.Fatalf("size changed: %q (%v)", out.String(), err)
				}
			default:
				want := map[string]string{"listremotes": "bd:\nzz:\n", "lsf": "one.txt\nsub/\n", "lsd": "sub/\n", "tree": "one.txt\nsub/\n  two.txt\n"}[cmd]
				if out.String() != want {
					t.Fatalf("text output changed: %q want %q", out.String(), want)
				}
			}
		})
	}
}

func TestLegacyPartialOutputStopsWrites(t *testing.T) {
	failure := errors.New("fixture partial output failure")
	writer := &failingCommandOutput{err: failure, allow: 1}
	err := runCommand(context.Background(), "listremotes", parsed{}, &Resolver{Aliases: map[string]uint64{"aa": 1, "bb": 2, "cc": 3}}, writer)
	if !errors.Is(err, failure) || writer.calls != 2 {
		t.Fatalf("partial output failure lost: err=%v writes=%d", err, writer.calls)
	}
}
