package uploader

import (
	"context"
	"sync"
)

// Each request owns its reader until finish cancels and joins any pending read.
// Late reads from a finished request cannot consume a retried part's buffer.
// Deliberately omit io.Closer: transport body closure must not cancel the
// request context while its response is still being received.
type uploadAttemptReader struct {
	SplitUnit
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
}

func newUploadAttemptReader(parent context.Context, unit SplitUnit) *uploadAttemptReader {
	ctx, cancel := context.WithCancel(parent)
	if setter, ok := unit.(interface{ setContext(context.Context) }); ok {
		setter.setContext(ctx)
	}
	return &uploadAttemptReader{SplitUnit: unit, ctx: ctx, cancel: cancel}
}

func (r *uploadAttemptReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.SplitUnit.Read(p)
}

func (r *uploadAttemptReader) finish() {
	r.cancel()
	r.mu.Lock()
	defer r.mu.Unlock()
}
