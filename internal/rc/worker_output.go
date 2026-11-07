package rc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

const workerFramePrefix = "@pcsclone "
const maxWorkerProtocolLine = 64 * 1024 // Excludes the newline delimiter.

var ErrWorkerProtocol = errors.New("invalid worker progress protocol")

// Legacy diagnostics are discarded; only bounded protocol frames are decoded.
// The output-copy goroutine owns these fields until Cmd.Wait has joined it.
type workerOutput struct {
	ctx                      context.Context
	pending                  []byte
	discarding               bool
	reset, checkpointFailed  bool
	callbackErr, protocolErr error
	stop                     func()
}

func (w *workerOutput) failProtocol(reason string) {
	if w.protocolErr != nil {
		return
	}
	w.protocolErr = fmt.Errorf("%w: %s", ErrWorkerProtocol, reason)
	if w.stop != nil {
		w.stop()
	}
}

func (w *workerOutput) report(v transferProgress) {
	if w.callbackErr != nil || w.protocolErr != nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			cause, ok := r.(error)
			if !ok {
				cause = fmt.Errorf("%v", r)
			}
			w.callbackErr = fmt.Errorf("%w: worker progress callback panic: %w", ErrProgressCallback, cause)
			if w.stop != nil {
				w.stop()
			}
		}
	}()
	reportProgress(w.ctx, v)
}

func (w *workerOutput) parseLine(line []byte, final bool) {
	prefix := []byte(workerFramePrefix)
	if !bytes.HasPrefix(line, prefix) {
		if final && len(line) > 0 && bytes.HasPrefix(prefix, line) {
			w.failProtocol("truncated frame prefix")
		}
		return
	}
	payload := bytes.TrimSpace(line[len(prefix):])
	var v transferProgress
	if len(payload) == 0 || payload[0] != '{' || json.Unmarshal(payload, &v) != nil {
		w.failProtocol("malformed frame")
		return
	}
	w.reset = w.reset || v.Reset
	w.checkpointFailed = w.checkpointFailed || v.CheckpointFailed
	if v.ProtocolFailed {
		w.failProtocol("worker rejected an outgoing frame")
	}
	// Failure suppresses callbacks, not final checkpoint diagnostics.
	w.report(v)
}

func (w *workerOutput) appendFragment(p []byte) {
	if w.discarding {
		return
	}
	if len(p) > maxWorkerProtocolLine-len(w.pending) {
		prefix := []byte(workerFramePrefix)
		protocol := bytes.HasPrefix(w.pending, prefix)
		if len(w.pending) < len(prefix) && bytes.HasPrefix(prefix, w.pending) {
			protocol = bytes.HasPrefix(p, prefix[len(w.pending):])
		}
		if protocol {
			w.failProtocol("frame exceeds line limit")
		}
		w.pending = w.pending[:0]
		w.discarding = true // Do not reinterpret a tail as a new physical line.
		return
	}
	needed := len(w.pending) + len(p)
	if needed > cap(w.pending) {
		capacity := min(maxWorkerProtocolLine, max(256, needed, 2*cap(w.pending)))
		next := make([]byte, len(w.pending), capacity)
		copy(next, w.pending)
		w.pending = next
	}
	w.pending = append(w.pending, p...)
}

func (w *workerOutput) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			w.appendFragment(p)
			break
		}
		w.appendFragment(p[:i])
		if !w.discarding {
			w.parseLine(w.pending, false)
		}
		w.pending = w.pending[:0]
		w.discarding = false
		p = p[i+1:]
	}
	// Continue draining after errors so the child can finish checkpoint cleanup.
	return n, nil
}

func (w *workerOutput) finish() {
	if !w.discarding && len(w.pending) > 0 {
		w.parseLine(w.pending, true)
	}
	w.pending = w.pending[:0]
	w.discarding = false
}
