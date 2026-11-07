package pcsclone

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func TestCancellationKeepsCallbackFailureDiagnostics(t *testing.T) {
	for _, callback := range []error{rc.ErrLogCallback, rc.ErrProgressCallback} {
		for _, wrapped := range []bool{false, true} {
			for _, checkpoint := range []bool{false, true} {
				for _, protocol := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/wrapped=%t/checkpoint=%t/protocol=%t", callback, wrapped, checkpoint, protocol), func(t *testing.T) {
						cause := errors.New("synthetic observer detail")
						err := errors.Join(context.Canceled, fmt.Errorf("%w: %w", callback, cause))
						if checkpoint {
							err = errors.Join(err, rc.ErrCheckpointSave)
						}
						if protocol {
							err = errors.Join(err, rc.ErrWorkerProtocol)
						}
						if wrapped {
							err = fmt.Errorf("command: %w", err)
						}
						for _, canceled := range []bool{false, true} {
							var stderr bytes.Buffer
							app := &App{Stderr: &stderr}
							code, want := 0, 1
							if canceled {
								code, want = app.reportCancellation(err), 130
							} else {
								code = app.report(err)
							}
							if code != want || !strings.Contains(stderr.String(), callback.Error()) || !strings.Contains(stderr.String(), cause.Error()) {
								t.Errorf("observer failure hidden or misclassified: code=%d want=%d stderr=%q", code, want, stderr.String())
							}
							if checkpoint && !strings.Contains(stderr.String(), rc.ErrCheckpointSave.Error()) || protocol && !strings.Contains(stderr.String(), rc.ErrWorkerProtocol.Error()) {
								t.Errorf("other failure diagnostic lost: %q", stderr.String())
							}
							if canceled && !strings.Contains(stderr.String(), "pcsclone: canceled") {
								t.Errorf("cancellation diagnostic lost: %q", stderr.String())
							}
						}
					})
				}
			}
		}
	}
}
