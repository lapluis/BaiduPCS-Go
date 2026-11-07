package uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type signaledReaderAt struct {
	*bytes.Reader
	started chan struct{}
}

func (s signaledReaderAt) ReadAt(p []byte, off int64) (int, error) {
	close(s.started)
	return s.Reader.ReadAt(p, off)
}

func TestUploadAttemptCloseCancelsDebtAndGatesLateReads(t *testing.T) {
	r := speeds.NewRateLimit(1)
	r.SetInterval(time.Hour)
	defer r.Stop()
	r.Add(1000000)
	started := make(chan struct{})
	unit := NewBufioSplitUnit(signaledReaderAt{bytes.NewReader([]byte("abcd")), started}, transfer.Range{Begin: 0, End: 4}, nil, r)
	attempt := newUploadAttemptReader(context.Background(), unit)
	done := make(chan error, 1)
	go func() { _, err := attempt.Read(make([]byte, 4)); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("read not started")
	}
	closed := make(chan struct{})
	go func() { attempt.finish(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close hung on debt")
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := unit.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := attempt.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) || unit.Readed() != 0 {
		t.Fatal("old attempt touched retry buffer", err, unit.Readed())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := r.AddWithContext(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close erased debt", err)
	}
}

func TestUploadFatalSourceErrorBypassesDebtWait(t *testing.T) {
	r := speeds.NewRateLimit(1)
	r.SetInterval(time.Hour)
	defer r.Stop()
	r.Add(1000000)
	unit := NewBufioSplitUnit(sourceReadBoundary{Reader: bytes.NewReader([]byte("ab"))}, transfer.Range{Begin: 0, End: 4}, nil, r)
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(unit); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSourceRead) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("source failure hidden behind quota wait")
	}
}

func TestUploadRetryJoinsPreviousQuotaReader(t *testing.T) {
	var u *MultiUploader
	var previous rio.ReaderLen64
	firstDone := make(chan error, 1)
	calls := 0
	backend := &lifecycleBackend{part: func(_ context.Context, _ int, r rio.ReaderLen64) (string, error) {
		calls++
		if calls == 1 {
			previous = r
			go func() { _, err := r.Read(make([]byte, 4)); firstDone <- err }()
			deadline := time.After(time.Second)
			for u.workers[0].splitUnit.Readed() == 0 {
				select {
				case <-deadline:
					return "", &MultiError{Terminated: true, Err: errors.New("reader did not start")}
				case <-time.After(time.Millisecond):
				}
			}
			return "", errors.New("fixture timeout before body read completed")
		}
		select {
		case err := <-firstDone:
			if !errors.Is(err, context.Canceled) {
				return "", &MultiError{Terminated: true, Err: errors.New("old read not canceled")}
			}
		default:
			return "", &MultiError{Terminated: true, Err: errors.New("retry overlapped old read")}
		}
		if _, err := previous.Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
			return "", &MultiError{Terminated: true, Err: errors.New("late old read consumed retry")}
		}
		u.rateLimit.Stop()
		data, err := io.ReadAll(r)
		if string(data) != "abcd" {
			return "", &MultiError{Terminated: true, Err: errors.New("retry did not rewind all bytes")}
		}
		return "completed", err
	}}
	u = NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcd"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4, MaxRate: 1}, "/fixture")
	u.OnExecute(func() { u.rateLimit.SetInterval(time.Hour); u.rateLimit.Add(1000000) })
	done := make(chan error, 1)
	go func() { done <- u.ExecuteWithError() }()
	select {
	case err := <-done:
		if err != nil || calls != 2 || backend.merged != 1 {
			t.Fatalf("calls=%d merged=%d err=%v", calls, backend.merged, err)
		}
	case <-time.After(4 * time.Second):
		u.Cancel()
		t.Fatal("retry hung on old quota reader")
	}
}
