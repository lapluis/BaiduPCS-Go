package uploader

import "fmt"

func runUploadEvent(name string, event func()) (err error) {
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
