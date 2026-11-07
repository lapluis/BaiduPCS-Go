package rc

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

type mkdirPreviewProbe struct {
	lookup func(string) (Entry, error)
	paths  []string
	writes int
}

func (*mkdirPreviewProbe) Identity() string { return "mkdir-preview-fixture" }

func (*mkdirPreviewProbe) List(context.Context, string) ([]Entry, error) {
	return nil, errors.New("unexpected list in preview validation")
}

func (*mkdirPreviewProbe) Hash(context.Context, string) (string, error) {
	return "", errors.New("unexpected hash in preview validation")
}

func (p *mkdirPreviewProbe) Remove(context.Context, string, bool) error {
	p.writes++
	return errors.New("unexpected removal in preview")
}

func (p *mkdirPreviewProbe) Stat(_ context.Context, name string) (Entry, error) {
	p.paths = append(p.paths, name)
	return p.lookup(name)
}

func (p *mkdirPreviewProbe) Mkdir(context.Context, string) error {
	p.writes++
	return errors.New("unexpected mkdir in preview")
}

func TestMkdirPreviewAncestorChecksHonorCancellation(t *testing.T) {
	for _, phase := range []string{"before", "deadline", "missing", "directory", "io-error"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.Canceled
			wantCalls := 1
			if phase == "before" {
				cancel()
				wantCalls = 0
			} else if phase == "deadline" {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				want, wantCalls = context.DeadlineExceeded, 0
			}
			cause := &os.PathError{Op: "stat", Path: "/parent/child", Err: os.ErrPermission}
			backend := &mkdirPreviewProbe{lookup: func(string) (Entry, error) {
				cancel()
				switch phase {
				case "directory":
					return Entry{IsDir: true}, nil
				case "io-error":
					return Entry{}, cause
				default:
					return Entry{}, os.ErrNotExist
				}
			}}
			err := ValidateMkdirTarget(ctx, Endpoint{FS: backend, Root: "/parent/child"})
			if !errors.Is(err, want) || len(backend.paths) != wantCalls || backend.writes != 0 {
				t.Fatalf("canceled preview continued: paths=%v writes=%d err=%v", backend.paths, backend.writes, err)
			}
			if phase == "io-error" && (!errors.Is(err, cause) || !errors.Is(err, os.ErrPermission)) {
				t.Fatalf("I/O cause lost alongside cancellation: %v", err)
			}
		})
	}
}

func TestMkdirPreviewStopsAtNearestExistingDirectory(t *testing.T) {
	backend := &mkdirPreviewProbe{lookup: func(name string) (Entry, error) {
		if name == "/existing" {
			return Entry{IsDir: true}, nil
		}
		return Entry{}, os.ErrNotExist
	}}
	err := ValidateMkdirTarget(context.Background(), Endpoint{FS: backend, Root: "/existing/missing/child"})
	want := []string{"/existing/missing/child", "/existing/missing", "/existing"}
	if err != nil || !reflect.DeepEqual(backend.paths, want) || backend.writes != 0 {
		t.Fatalf("ancestor traversal changed: paths=%v writes=%d err=%v", backend.paths, backend.writes, err)
	}
}
