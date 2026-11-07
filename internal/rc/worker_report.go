package rc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checkedwriter"
)

type workerReporter struct {
	mu         sync.Mutex
	output     *checkedwriter.Writer
	cancel     context.CancelFunc
	cancelOnce sync.Once
	err        error // Protected by mu, including encoding/size errors before Write.
}

func newWorkerReporter(output io.Writer, cancel context.CancelFunc) *workerReporter {
	return &workerReporter{output: checkedwriter.New(output), cancel: cancel}
}

func (r *workerReporter) Report(v transferProgress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return
	}
	if len(v.ETag) > maxWorkerProtocolLine || len(v.Modified) > maxWorkerProtocolLine || len(v.ETag)+len(v.Modified) > maxWorkerProtocolLine {
		r.rejectOversized()
		return
	}
	b, err := json.Marshal(v)
	if err == nil && len(workerFramePrefix)+len(b) > maxWorkerProtocolLine {
		r.rejectOversized()
		return
	}
	if err == nil {
		_, err = fmt.Fprintf(r.output, "%s%s\n", workerFramePrefix, b)
	}
	if err != nil {
		r.err = err
		// Losing progress or checkpoint diagnostics must stop the transfer.
		// The checked writer preserves the first failure and rejects retries.
		r.cancelOnce.Do(func() {
			if r.cancel != nil {
				r.cancel()
			}
		})
	}
}

// The transport is still usable on a size failure. Send a small, bounded
// diagnostic instead of silently truncating identity metadata or losing it.
func (r *workerReporter) rejectOversized() {
	r.err = fmt.Errorf("%w: outgoing frame exceeds line limit", ErrWorkerProtocol)
	_, writeErr := fmt.Fprintf(r.output, "%s{\"ProtocolFailed\":true}\n", workerFramePrefix)
	r.err = errors.Join(r.err, writeErr)
	r.cancelOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
}

func (r *workerReporter) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}
