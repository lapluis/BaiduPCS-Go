// Package checkedwriter tracks output errors across formatting calls.
package checkedwriter

import (
	"io"
	"sync"
)

// Writer retains the first write error and rejects subsequent writes.
// It also converts a short write without an error into io.ErrShortWrite.
type Writer struct {
	mu     sync.Mutex
	target io.Writer
	err    error
}

// New wraps target without taking ownership of it.
func New(target io.Writer) *Writer { return &Writer{target: target} }

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	n, err := w.target.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

// Err returns the first output error, or nil after successful writes.
func (w *Writer) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}
