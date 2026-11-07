package downloader

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrEventCallback identifies a failed download lifecycle or control callback.
var ErrEventCallback = errors.New("download event callback failed")

// ErrProgressCallback identifies a failed download progress observer.
var ErrProgressCallback = errors.New("download progress callback failed")

func runDownloadEvent(name string, event func()) (err error) {
	if event == nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			cause, ok := r.(error)
			if !ok {
				cause = fmt.Errorf("%v", r)
			}
			err = fmt.Errorf("%w (%s): %w", ErrEventCallback, name, cause)
		}
	}()
	event()
	return nil
}

// Registration and errors are protected by Downloader.cancelMu. Sealing under
// that lock prevents WaitGroup.Add from racing a wait on an empty group.
type downloadControlEvents struct {
	accepting bool
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	err       error
}

func (der *Downloader) prepareControlEvent(name string, event func()) func() {
	der.cancelMu.Lock()
	defer der.cancelMu.Unlock()
	return der.prepareControlEventLocked(name, event)
}

func (der *Downloader) prepareControlEventLocked(name string, event func()) func() {
	controls := der.controlEvents
	if controls == nil || !controls.accepting || event == nil {
		return nil
	}
	controls.wg.Add(1)
	return func() {
		defer controls.wg.Done()
		if err := runDownloadEvent(name, event); err != nil {
			der.cancelMu.Lock()
			controls.err = errors.Join(controls.err, err)
			der.cancelMu.Unlock()
			// Stop this execution without recursively notifying OnCancel.
			controls.cancel()
		}
	}
}

func (der *Downloader) joinControlEvents(controls *downloadControlEvents) error {
	der.cancelMu.Lock()
	controls.accepting = false
	der.cancelMu.Unlock()
	controls.wg.Wait()
	der.cancelMu.Lock()
	defer der.cancelMu.Unlock()
	return controls.err
}
