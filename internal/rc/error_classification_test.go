package rc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRequestDeadlineWithLiveParentIsFailure(t *testing.T) {
	for name, wrap := range map[string]func(error) error{
		"direct":  func(err error) error { return err },
		"network": func(err error) error { return &net.OpError{Op: "read", Net: "fixture", Err: err} },
		"pcs":     func(err error) error { e := pcserror.NewPCSErrorInfo("fixture"); e.SetNetError(err); return e },
	} {
		t.Run(name, func(t *testing.T) {
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("fixture")}, id: 101}
			src := cloud.endpoint(1, "/Temp/a")
			entry, err := src.FS.Stat(context.Background(), src.Root)
			if err != nil {
				t.Fatal(err)
			}
			p := &Plan{Source: src, Destination: Endpoint{&Local{}, filepath.Join(t.TempDir(), "a")}, Actions: []Action{{Kind: "copy", Source: &entry}}}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			runTransferWorker = func(context.Context, string, uint64, string, string, int64) error {
				return fmt.Errorf("request timed out: %w", wrap(context.DeadlineExceeded))
			}
			errorLogs := 0
			o := opts()
			o.Emit = func(kind, _ string) {
				if kind == "error" {
					errorLogs++
				}
			}
			result, err := Execute(context.Background(), p, o)
			if !errors.Is(err, context.DeadlineExceeded) || result.Failed != 1 || result.Canceled != 0 || result.Copied != 0 || result.Pending != 0 || errorLogs != 1 {
				t.Fatalf("request timeout misclassified: result=%+v err=%v errorLogs=%d", result, err, errorLogs)
			}
		})
	}
}

func TestParentDeadlineRemainsCancellationUnlessCheckpointFails(t *testing.T) {
	for _, checkpointFails := range []bool{false, true} {
		t.Run(fmt.Sprint(checkpointFails), func(t *testing.T) {
			cloud := &fakeCloud{files: map[string][]byte{"/Temp/a": []byte("fixture")}, id: 101}
			src := cloud.endpoint(1, "/Temp/a")
			entry, err := src.FS.Stat(context.Background(), src.Root)
			if err != nil {
				t.Fatal(err)
			}
			p := &Plan{Source: src, Destination: Endpoint{&Local{}, filepath.Join(t.TempDir(), "a")}, Actions: []Action{{Kind: "copy", Source: &entry}}}
			previous := runTransferWorker
			defer func() { runTransferWorker = previous }()
			calls := 0
			runTransferWorker = func(ctx context.Context, _ string, _ uint64, _, _ string, _ int64) error {
				calls++
				<-ctx.Done()
				if checkpointFails {
					return errors.Join(ctx.Err(), ErrCheckpointSave)
				}
				return ctx.Err()
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := Execute(ctx, p, opts())
			wantFailed, wantCanceled := 0, 1
			if checkpointFails {
				wantFailed, wantCanceled = 1, 0
			}
			if calls != 1 || !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCheckpointSave) != checkpointFails || result.Failed != wantFailed || result.Canceled != wantCanceled || result.Pending != 0 {
				t.Fatalf("parent deadline classification: result=%+v err=%v calls=%d", result, err, calls)
			}
		})
	}
}
