package rc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBatchDeletionRechecksEachFile(t *testing.T) {
	for _, mode := range []string{"delete", "purge", "sync"} {
		t.Run(mode, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, d, "a", "old a")
			write(t, d, "b", "old b")
			o := opts()
			o.Emit = func(kind, p string) {
				if kind == "delete" && p == "a" {
					write(t, d, "b", "new external data")
				}
			}
			var err error
			if mode == "sync" {
				var plan *Plan
				plan, err = BuildPlan(context.Background(), s, d, mode, o)
				if err == nil {
					_, err = Execute(context.Background(), plan, o)
				}
			} else {
				err = Delete(context.Background(), d, mode, o)
			}
			if err == nil || !strings.Contains(err.Error(), "changed after planning") {
				t.Fatal("changed candidate not rejected", err)
			}
			data, readErr := os.ReadFile(d.At("b"))
			if readErr != nil || string(data) != "new external data" {
				t.Fatal("updated candidate lost", string(data), readErr)
			}
			if _, err = os.Stat(d.At("a")); !os.IsNotExist(err) {
				t.Fatal("first deletion not exercised", err)
			}
		})
	}
}

type changedVerificationTarget struct {
	Local
	change func(string) error
}

func (b *changedVerificationTarget) Hash(ctx context.Context, p string) (string, error) {
	digest, err := b.Local.Hash(ctx, p)
	if err != nil {
		return "", err
	}
	if err = b.change(p); err != nil {
		return "", err
	}
	return digest, nil
}
func TestMoveRetainsSourceWhenTargetChangesDuringHash(t *testing.T) {
	changes := map[string]func(string) error{
		"size":    func(p string) error { return os.WriteFile(p, []byte("external replacement"), 0600) },
		"mtime":   func(p string) error { future := time.Now().Add(time.Hour); return os.Chtimes(p, future, future) },
		"removed": os.Remove,
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			s, d := endpoints(t)
			write(t, s, "file", "good copy")
			write(t, d, "file", "good copy")
			entry, err := s.FS.Stat(context.Background(), s.At("file"))
			if err != nil {
				t.Fatal(err)
			}
			d.FS = &changedVerificationTarget{change: change}
			err = verifyAndRemove(context.Background(), s, d, Action{Path: "file", Source: &entry}, opts())
			if err == nil || !strings.Contains(err.Error(), "source retained") {
				t.Fatal("changed target accepted", err)
			}
			data, err := os.ReadFile(s.At("file"))
			if err != nil || string(data) != "good copy" {
				t.Fatal("source lost", string(data), err)
			}
		})
	}
}
