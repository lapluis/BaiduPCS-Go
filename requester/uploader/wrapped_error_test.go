package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

func TestWrappedPartErrorsStopWithoutRetryOrMerge(t *testing.T) {
	for name, wrap := range map[string]func(error) error{
		"pcs":   func(err error) error { e := pcserror.NewPCSErrorInfo("part"); e.SetNetError(err); return e },
		"multi": func(err error) error { return &MultiError{Err: err} },
	} {
		for _, cause := range []error{context.Canceled, rio.ErrSourceChanged} {
			t.Run(name+"/"+cause.Error(), func(t *testing.T) {
				calls := 0
				guard := errors.New("unexpected retry")
				backend := &lifecycleBackend{part: func(context.Context, int, rio.ReaderLen64) (string, error) {
					calls++
					if calls > 1 {
						return "", &MultiError{Err: guard, Terminated: true}
					}
					return "", fmt.Errorf("part failed: %w", wrap(cause))
				}}
				u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
				var got error
				var canceled, succeeded bool
				u.OnError(func(err error) { got = err })
				u.OnCancel(func() { canceled = true })
				u.OnSuccess(func() { succeeded = true })
				u.Execute()
				if calls != 1 || backend.merged != 0 || succeeded {
					t.Fatalf("terminal cause retried or merged: calls=%d merges=%d success=%v error=%v", calls, backend.merged, succeeded, got)
				}
				if cause == context.Canceled {
					if !canceled || got != nil {
						t.Fatalf("cancellation lost: canceled=%v error=%v", canceled, got)
					}
				} else if canceled || !errors.Is(got, cause) {
					t.Fatalf("source error lost: canceled=%v error=%v", canceled, got)
				}
				for _, block := range u.InstanceState().BlockList {
					if block.CheckSum != "" {
						t.Fatal("failed part marked complete")
					}
				}
			})
		}
	}
}
