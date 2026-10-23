package rc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCheckDetectsDirectoryFileConflict(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		s, d := endpoints(t)
		if err := os.Mkdir(s.At("item"), 0755); err != nil {
			t.Fatal(err)
		}
		write(t, d, "item", "unexpected data")
		if reverse {
			s, d = d, s
		}
		if err := Check(context.Background(), s, d, opts()); err == nil {
			t.Fatal("type conflict reported as equal")
		}
		o := opts()
		o.Filter, _ = NewFilter(nil, []string{"item"}, "")
		if err := Check(context.Background(), s, d, o); err != nil {
			t.Fatal("excluded conflict reported", err)
		}
	}
}

func TestCopyIgnoresExcludedDirectoryConflict(t *testing.T) {
	for _, createDirs := range []bool{false, true} {
		s, d := endpoints(t)
		write(t, s, "ignored/child", "excluded")
		write(t, s, "keep.txt", "keep")
		write(t, d, "ignored", "retain")
		o := opts()
		o.CreateEmptyDirs = createDirs
		o.Filter, _ = NewFilter(nil, []string{"ignored"}, "")
		p, err := BuildPlan(context.Background(), s, d, "copy", o)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = Execute(context.Background(), p, o); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{"ignored": "retain", "keep.txt": "keep"} {
			got, err := os.ReadFile(d.At(name))
			if err != nil || string(got) != want {
				t.Fatalf("%s: %q, %v", name, got, err)
			}
		}
		o.Filter = Filter{}
		if _, err = BuildPlan(context.Background(), s, d, "copy", o); err == nil {
			t.Fatal("selected child with blocked parent accepted")
		}
	}
}

type retryHashBackend struct {
	Local
	failNext      bool
	failures      int
	beforeFailure func()
}

func (b *retryHashBackend) Hash(ctx context.Context, p string) (string, error) {
	if b.failNext {
		b.failNext = false
		b.failures++
		if b.beforeFailure != nil {
			b.beforeFailure()
		}
		return "", errors.New("temporary hash failure")
	}
	return b.Local.Hash(ctx, p)
}

func TestRetryAfterChecksumError(t *testing.T) {
	for _, mode := range []string{"copy", "move"} {
		for _, existing := range []bool{false, true} {
			name := mode + "/new"
			if existing {
				name = mode + "/overwrite"
			}
			t.Run(name, func(t *testing.T) {
				s, d := endpoints(t)
				write(t, s, "file", "data")
				if existing {
					write(t, d, "file", "old!")
				}
				backend := &retryHashBackend{}
				s.FS = backend
				o := opts()
				o.Checksum = true
				o.Retries = 1
				p, err := BuildPlan(context.Background(), s, d, mode, o)
				if err != nil {
					t.Fatal(err)
				}
				backend.failNext = true
				result, err := Execute(context.Background(), p, o)
				if err != nil || result.Copied != 1 || result.Failed != 0 {
					t.Fatal(result, err)
				}
				if backend.failures != 1 {
					t.Fatal("verification failure not exercised")
				}
				got, err := os.ReadFile(d.At("file"))
				if err != nil || string(got) != "data" {
					t.Fatal(string(got), err)
				}
				_, err = os.Stat(s.At("file"))
				if mode == "move" {
					if !os.IsNotExist(err) {
						t.Fatal("source not removed", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestRetryPreservesDestinationChangedAfterWrite(t *testing.T) {
	s, d := endpoints(t)
	write(t, s, "file", "data")
	backend := &retryHashBackend{}
	s.FS = backend
	o := opts()
	o.Checksum = true
	o.Retries = 1
	p, err := BuildPlan(context.Background(), s, d, "move", o)
	if err != nil {
		t.Fatal(err)
	}
	backend.failNext = true
	backend.beforeFailure = func() { write(t, d, "file", "external replacement") }
	_, err = Execute(context.Background(), p, o)
	if err == nil || !strings.Contains(err.Error(), "changed after planning") {
		t.Fatal(err)
	}
	got, err := os.ReadFile(d.At("file"))
	if err != nil || string(got) != "external replacement" {
		t.Fatal(string(got), err)
	}
	if _, err = os.Stat(s.At("file")); err != nil {
		t.Fatal("source removed", err)
	}
}
