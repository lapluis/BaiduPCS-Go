package uploader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type finalStatusBackend struct {
	resumeBackend
	statusStarted <-chan struct{}
}

func (b *finalStatusBackend) Precreate(ctx context.Context) (string, pcserror.Error) {
	select {
	case <-b.statusStarted:
	case <-time.After(8 * time.Second):
		err := pcserror.NewPCSErrorInfo("fixture status synchronization")
		err.SetNetError(errors.New("status callback did not start"))
		return "", err
	}
	return b.resumeBackend.Precreate(ctx)
}

func TestFinalStatusCannotHideCancellationOrSourceChanges(t *testing.T) {
	for _, mode := range []string{"healthy", "cancel", "stop", "changed", "shrunk", "closed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), "source")
			content := "aaaabbbb"
			if err := os.WriteFile(source, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			stamp := time.Unix(1700000000, 0)
			if err := os.Chtimes(source, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			started := make(chan struct{})
			backend := &finalStatusBackend{statusStarted: started}
			u := NewMultiUploader(backend, rio.NewFileReaderAtLen64(file), &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			var mutationErr, inspectionCause error
			statusExited := false
			u.OnUploadStatusEvent(func(Status, <-chan struct{}) {
				if statusExited {
					return
				}
				close(started)
				// finish closes this only after the transfer and its final source
				// check, then waits for this callback before reporting success.
				<-u.finished
				switch mode {
				case "cancel":
					u.Cancel()
				case "stop":
					u.GracefulStop()
				case "changed", "shrunk":
					content = "ccccdddd"
					if mode == "shrunk" {
						content = "cc"
					}
					mutationErr = os.WriteFile(source, []byte(content), 0600)
					if mutationErr == nil {
						mutationErr = os.Chtimes(source, stamp.Add(time.Second), stamp.Add(time.Second))
					}
				case "closed":
					mutationErr = file.Close()
					_, inspectionCause = file.Stat()
					var pathErr *os.PathError
					if errors.As(inspectionCause, &pathErr) {
						inspectionCause = pathErr.Err
					}
				}
				statusExited = true
			})
			var observed error
			succeeded, canceled, failed, finished := 0, 0, 0, 0
			u.OnSuccess(func() { succeeded++ })
			u.OnCancel(func() { canceled++ })
			u.OnError(func(err error) { failed++; observed = err })
			u.OnFinish(func() { finished++ })
			outcome := u.ExecuteWithError()
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if !statusExited || finished != 1 || backend.merged != 1 || backend.uploaded != 2 {
				t.Fatalf("lifecycle did not join the completed transfer: status=%t finished=%d backend=%+v", statusExited, finished, backend)
			}
			switch mode {
			case "healthy":
				if outcome != nil || succeeded != 1 || canceled != 0 || failed != 0 {
					t.Fatalf("healthy completion changed: err=%v success=%d cancel=%d failure=%d", outcome, succeeded, canceled, failed)
				}
			case "cancel", "stop":
				if !errors.Is(outcome, context.Canceled) || succeeded != 0 || canceled != 1 || failed != 0 {
					t.Fatalf("late cancellation reported success: err=%v success=%d cancel=%d failure=%d", outcome, succeeded, canceled, failed)
				}
			default:
				if !errors.Is(outcome, rio.ErrSourceChanged) || !errors.Is(observed, rio.ErrSourceChanged) || succeeded != 0 || canceled != 0 || failed != 1 {
					t.Fatalf("late source failure reported success: returned=%v observed=%v success=%d cancel=%d failure=%d", outcome, observed, succeeded, canceled, failed)
				}
				if inspectionCause != nil && !errors.Is(outcome, inspectionCause) {
					t.Fatalf("source inspection cause lost: %v", outcome)
				}
			}
			if got, err := os.ReadFile(source); err != nil || string(got) != content {
				t.Fatalf("completion changed source: %q %v", got, err)
			}
			state := u.InstanceState()
			if len(state.BlockList) != 2 {
				t.Fatalf("completion lost part state: %+v", state)
			}
			for _, block := range state.BlockList {
				if block.CheckSum == "" {
					t.Fatal("completed part checksum lost")
				}
			}
		})
	}
}
