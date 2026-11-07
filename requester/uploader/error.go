package uploader

import "errors"

// ErrSourceRead identifies failures reading upload data, rather than sending it.
var ErrSourceRead = errors.New("upload source read failed")

// ErrProgressCallback identifies a failed asynchronous upload status callback.
var ErrProgressCallback = errors.New("upload progress callback failed")

// ErrEventCallback identifies a failed synchronous upload lifecycle callback.
var ErrEventCallback = errors.New("upload event callback failed")

// ErrPartRetryExhausted identifies a part that failed all bounded attempts.
var ErrPartRetryExhausted = errors.New("upload part retry limit reached")

type (
	// MultiError 多线程上传的错误
	MultiError struct {
		Err error
		// IsRetry 是否重试,
		Terminated bool
	}
)

func (me *MultiError) Error() string {
	return me.Err.Error()
}

// Unwrap preserves terminal causes such as cancellation and source changes.
func (me *MultiError) Unwrap() error { return me.Err }
