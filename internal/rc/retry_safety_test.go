package rc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/pcsfunctions/pcsdownload"
)

func TestTransferDoesNotRetryStateOrObserverFailure(t *testing.T) {
	for name, marker := range map[string]error{
		"checkpoint": ErrCheckpointSave, "protocol": ErrWorkerProtocol,
		"progress": ErrProgressCallback, "logging": ErrLogCallback,
	} {
		for _, move := range []bool{false, true} {
			for _, wrap := range []string{"direct", "wrapped", "joined-cancel"} {
				t.Run(fmt.Sprintf("%s/move=%t/%s", name, move, wrap), func(t *testing.T) {
					cause := errors.New("first attempt failed")
					failure := errors.Join(marker, cause)
					if wrap == "wrapped" {
						failure = fmt.Errorf("outer: %w", failure)
					} else if wrap == "joined-cancel" {
						failure = errors.Join(context.Canceled, failure)
					}
					runTransferRetryFixture(t, move, failure, marker, cause, false, 1)
				})
			}
		}
	}
}

func TestTransferStillRetriesOrdinaryFailure(t *testing.T) {
	for _, retries := range []int{0, 1} {
		t.Run(fmt.Sprint(retries), func(t *testing.T) {
			failure := errors.New("temporary transfer failure")
			runTransferRetryFixture(t, false, failure, nil, failure, retries > 0, retries)
		})
	}
}

func runTransferRetryFixture(t *testing.T, move bool, failure, marker, cause error, succeeds bool, retries int) {
	t.Helper()
	cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("abcdefgh")}, id: 101}
	src := cloud.endpoint(7, "/Temp/a")
	target := filepath.Join(t.TempDir(), "result")
	if err := os.WriteFile(target, []byte("old target"), 0600); err != nil {
		t.Fatal(err)
	}
	entry, err := src.FS.Stat(context.Background(), src.Root)
	if err != nil {
		t.Fatal(err)
	}
	local := &Local{}
	old, err := local.Stat(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Source: src, Destination: Endpoint{local, target}, Move: move,
		Actions: []Action{{Kind: "copy", Source: &entry, Destination: &old}}}
	previous := runTransferWorker
	defer func() { runTransferWorker = previous }()
	calls := 0
	var partial string
	runTransferWorker = func(ctx context.Context, kind string, _ uint64, _, to string, _ int64) error {
		if kind == "probe" {
			reportProgress(ctx, transferProgress{Total: 8, ETag: `"fixture"`})
			return nil
		}
		calls++
		if calls == 1 {
			partial = to
			reportProgress(ctx, transferProgress{ETag: `"fixture"`})
			if err := os.WriteFile(to, []byte("abc"), 0600); err != nil {
				return err
			}
			if err := os.WriteFile(to+pcsdownload.DownloadSuffix, []byte("saved range state"), 0600); err != nil {
				return err
			}
			return failure
		}
		return os.WriteFile(to, []byte("abcdefgh"), 0600)
	}
	o := opts()
	o.Transfers, o.Retries = 1, retries
	result, runErr := Execute(context.Background(), p, o)
	if succeeds {
		if runErr != nil || calls != 2 || result.Copied != 1 || result.Failed != 0 {
			t.Fatalf("ordinary retry failed: result=%+v err=%v calls=%d", result, runErr, calls)
		}
		if got, err := os.ReadFile(target); err != nil || string(got) != "abcdefgh" {
			t.Fatalf("retry target=%q err=%v", got, err)
		}
		return
	}
	if !errors.Is(runErr, cause) || marker != nil && !errors.Is(runErr, marker) || calls != 1 || result.Failed != 1 || result.Copied != 0 || result.Canceled != 0 {
		t.Fatalf("failure retried or lost: result=%+v err=%v calls=%d", result, runErr, calls)
	}
	if string(cloud.files[src.Root]) != "abcdefgh" {
		t.Fatal("failed move removed source")
	}
	for name, want := range map[string]string{target: "old target", partial: "abc", partial + pcsdownload.DownloadSuffix: "saved range state"} {
		if got, err := os.ReadFile(name); err != nil || string(got) != want {
			t.Errorf("file %s=%q err=%v, want %q", name, got, err, want)
		}
	}
}

func TestStageSaveErrorRetainsClassificationAndFiles(t *testing.T) {
	f := newStageReadFixture(t)
	// Renaming a regular checkpoint onto a directory reliably fails on all OSes.
	f.stage.record = t.TempDir()
	err := f.stage.save()
	var linkErr *os.LinkError
	if !errors.Is(err, ErrCheckpointSave) || !errors.As(err, &linkErr) {
		t.Fatalf("checkpoint failure lost classification or cause: %v", err)
	}
	f.checkPreservedAndRetry(t)
}
