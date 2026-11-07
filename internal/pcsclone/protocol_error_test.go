package pcsclone

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/internal/rc"
)

func TestCancellationReportsWorkerProtocolFailure(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		var stderr bytes.Buffer
		app := &App{Stderr: &stderr}
		err := errors.Join(context.Canceled, rc.ErrWorkerProtocol)
		if checkpoint {
			err = errors.Join(err, rc.ErrCheckpointSave)
		}
		if code := app.reportCancellation(err); code != 130 || !strings.Contains(stderr.String(), rc.ErrWorkerProtocol.Error()) || !strings.Contains(stderr.String(), "canceled") {
			t.Fatalf("protocol failure hidden by cancellation: code=%d stderr=%q", code, stderr.String())
		}
		if checkpoint && !strings.Contains(stderr.String(), rc.ErrCheckpointSave.Error()) {
			t.Fatalf("checkpoint failure hidden: %q", stderr.String())
		}
	}
}
