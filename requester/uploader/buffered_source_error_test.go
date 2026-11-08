package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/multipartreader"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type completeErrorSource struct {
	*bytes.Reader
	failure error
}

func (r completeErrorSource) Len() int64 { return r.Size() }
func (r completeErrorSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.Reader.ReadAt(p, off)
	if n == len(p) && off+int64(n) == r.Size() {
		return n, r.failure
	}
	return n, err
}

func TestMultipartUploadPreservesBufferedTerminalSourceError(t *testing.T) {
	for _, size := range []int{4, BufioReadSize, BufioReadSize + 1} {
		for _, failure := range []error{errors.New("fixture source I/O failure"), io.EOF, nil} {
			name := "healthy"
			if failure != nil {
				name = failure.Error()
			}
			t.Run(fmt.Sprintf("size=%d/%s", size, name), func(t *testing.T) {
				calls := 0
				backend := &lifecycleBackend{part: func(_ context.Context, _ int, r rio.ReaderLen64) (string, error) {
					calls++
					body := multipartreader.NewMultipartReader()
					body.AddFormFile("file", "fixture.bin", r)
					if err := body.CloseMultipart(); err != nil {
						return "", err
					}
					_, err := io.Copy(io.Discard, body)
					return "fixture-checksum", err
				}}
				u := NewMultiUploader(backend, completeErrorSource{bytes.NewReader(bytes.Repeat([]byte{'a'}, size)), failure}, &MultiUploaderConfig{Parallel: 1, BlockSize: int64(size)}, "/fixture")
				succeeded := false
				u.OnSuccess(func() { succeeded = true })
				err := u.ExecuteWithError()
				fatal := failure != nil && failure != io.EOF
				if calls != 1 {
					t.Fatalf("source failure retried: calls=%d", calls)
				}
				if fatal {
					if !errors.Is(err, failure) || !errors.Is(err, ErrSourceRead) || succeeded || backend.merged != 0 {
						t.Fatalf("terminal source error lost: err=%v success=%t merged=%d", err, succeeded, backend.merged)
					}
					for _, block := range u.InstanceState().BlockList {
						if block.CheckSum != "" {
							t.Fatal("failed source part was marked complete")
						}
					}
				} else if err != nil || !succeeded || backend.merged != 1 {
					t.Fatalf("complete source rejected: err=%v success=%t merged=%d", err, succeeded, backend.merged)
				}
			})
		}
	}
}
