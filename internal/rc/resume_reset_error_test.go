package rc

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestDownloadIdentityResetFailureStopsRetries(t *testing.T) {
	for _, mode := range []string{"copy", "move", "sync"} {
		for _, blocked := range []string{"payload", "ranges", "record"} {
			t.Run(mode+"/"+blocked, func(t *testing.T) {
				content := []byte("abcdefgh")
				cloud := &fakeCloud{files: map[string][]byte{"/Temp/file": content}, id: 101}
				src := cloud.endpoint(7, "/Temp")
				dst := Endpoint{FS: &Local{}, Root: t.TempDir()}
				write(t, dst, "file", "old")
				write(t, dst, "extra", "retain")
				o := opts()
				o.Retries = 2
				entry, err := src.FS.Stat(context.Background(), src.At("file"))
				if err != nil {
					t.Fatal(err)
				}
				old, err := dst.FS.Stat(context.Background(), dst.At("file"))
				if err != nil {
					t.Fatal(err)
				}
				plan := &Plan{Source: src, Destination: dst, Move: mode == "move", Actions: []Action{{Kind: "copy", Path: "file", Source: &entry, Destination: &old}}}
				if mode == "sync" {
					extra, err := dst.FS.Stat(context.Background(), dst.At("extra"))
					if err != nil {
						t.Fatal(err)
					}
					plan.Actions = append(plan.Actions, Action{Kind: "delete", Path: "extra", Destination: &extra})
				}
				stage, err := stageFor(context.Background(), src.FS.(*Remote), src.At("file"), dst.At("file"), entry)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stage.payload, []byte("abc"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(stage.payload+pcsdownload.DownloadSuffix, []byte("range state"), 0600); err != nil {
					t.Fatal(err)
				}
				stage.info.ETag = `"old-entity"`
				if err := stage.save(); err != nil {
					t.Fatal(err)
				}
				path := stage.payload
				if blocked == "ranges" {
					path += pcsdownload.DownloadSuffix
				}
				if blocked == "record" {
					path = stage.record
				}
				failure := errors.New("fixture cannot discard stale partial")
				previousRemove, previousWorker := removeDownloadState, runTransferWorker
				defer func() { removeDownloadState = previousRemove; runTransferWorker = previousWorker }()
				removals, calls := 0, 0
				removeDownloadState = func(p string) error {
					if p == path {
						removals++
						return &os.PathError{Op: "remove", Path: p, Err: failure}
					}
					return os.Remove(p)
				}
				runTransferWorker = func(context.Context, string, uint64, string, string, int64) error { calls++; return errResumeIdentity }
				result, err := Execute(context.Background(), plan, o)
				if !errors.Is(err, ErrCheckpointSave) || !errors.Is(err, failure) || !errors.Is(err, errResumeIdentity) || calls != 1 || removals != 1 || result.Failed != 1 || result.Copied != 0 || result.Deleted != 0 {
					t.Fatalf("reset failure lost or retried: calls=%d removals=%d result=%+v err=%v", calls, removals, result, err)
				}
				for name, want := range map[string]string{"file": "old", "extra": "retain"} {
					b, err := os.ReadFile(dst.At(name))
					if err != nil || string(b) != want {
						t.Fatal("destination changed", name, err)
					}
				}
				if _, err := src.FS.Stat(context.Background(), src.At("file")); err != nil {
					t.Fatal("source removed", err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatal("blocked state removed", err)
				}
			})
		}
	}
}

func TestDownloadPrepareRequiredResetPreservesIOFailure(t *testing.T) {
	f := newStageReadFixture(t)
	previous := removeDownloadState
	defer func() { removeDownloadState = previous }()
	failure := errors.New("fixture reset denied")
	removeDownloadState = func(p string) error {
		if p == f.stage.payload {
			return &os.PathError{Op: "remove", Path: p, Err: failure}
		}
		return os.Remove(p)
	}
	changed := f.entry
	changed.ID = "different-source"
	_, err := stageFor(context.Background(), f.remote, "/file", f.target, changed)
	if !errors.Is(err, ErrCheckpointSave) || !errors.Is(err, failure) {
		t.Fatal("prepare hid required reset failure", err)
	}
	for path, want := range f.before {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatal("failed preparation discarded state", path, err)
		}
	}
}
