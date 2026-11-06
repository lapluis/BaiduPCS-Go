package uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type failingSourceReader struct{ failure error }

func (r failingSourceReader) Len() int64                        { return 8 }
func (r failingSourceReader) ReadAt([]byte, int64) (int, error) { return 0, r.failure }

func TestUploadSourceReadFailureStopsWithoutPartRetry(t *testing.T) {
	failure := errors.New("fixture source I/O failure")
	calls := 0
	backend := &lifecycleBackend{part: func(_ context.Context, _ int, r rio.ReaderLen64) (string, error) {
		calls++
		if calls > 1 {
			return "", &MultiError{Err: errors.New("unexpected part retry"), Terminated: true}
		}
		_, err := io.ReadAll(r)
		wrapped := pcserror.NewPCSErrorInfo("fixture part upload")
		wrapped.SetNetError(err)
		return "", wrapped
	}}
	u := NewMultiUploader(backend, failingSourceReader{failure}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	var got error
	succeeded := false
	u.OnError(func(err error) { got = err })
	u.OnSuccess(func() { succeeded = true })
	u.Execute()
	if calls != 1 || !errors.Is(got, failure) || succeeded || backend.merged != 0 {
		t.Fatalf("source error retried or lost: calls=%d err=%v success=%v merged=%d", calls, got, succeeded, backend.merged)
	}
	for _, block := range u.InstanceState().BlockList {
		if block.CheckSum != "" {
			t.Fatal("failed source part marked complete")
		}
	}
}

type sourceReadBoundary struct {
	*bytes.Reader
	shortNil, fullEOF bool
}

func (r sourceReadBoundary) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.Reader.ReadAt(p, off)
	if r.shortNil {
		err = nil
	}
	if r.fullEOF && n == len(p) {
		err = io.EOF
	}
	return n, err
}

func TestSplitSourceReadErrorsAndCompleteEOF(t *testing.T) {
	for _, mode := range []string{"short-eof", "short-nil", "complete-eof"} {
		t.Run(mode, func(t *testing.T) {
			data := []byte("abcd")
			if mode == "complete-eof" {
				data = []byte("abcdefgh")
			}
			reader := sourceReadBoundary{Reader: bytes.NewReader(data), shortNil: mode == "short-nil", fullEOF: mode == "complete-eof"}
			unit := NewBufioSplitUnit(reader, transfer.Range{Begin: 0, End: 8}, nil, nil)
			got, err := io.ReadAll(unit)
			if !bytes.Equal(got, data) {
				t.Fatalf("source bytes changed: %q", got)
			}
			if mode == "complete-eof" {
				if err != nil {
					t.Fatal("complete final EOF rejected:", err)
				}
			} else if !errors.Is(err, ErrSourceRead) {
				t.Fatalf("incomplete source accepted: %v", err)
			}
		})
	}
}
