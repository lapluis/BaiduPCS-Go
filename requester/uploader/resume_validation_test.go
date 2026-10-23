package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type resumeReader struct{ *bytes.Reader }

func (r resumeReader) Len() int64 { return r.Size() }

type resumeBackend struct {
	mu                           sync.Mutex
	precreated, uploaded, merged int
	parts                        map[int][]byte
	checksums                    map[int]string
}

func (b *resumeBackend) Precreate() (string, pcserror.Error) { b.precreated++; return "synthetic", nil }
func (b *resumeBackend) TmpFile(_ context.Context, _ string, _ string, id int, _ int64, r rio.ReaderLen64) (string, error) {
	data, err := io.ReadAll(r)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.uploaded++
	if b.parts == nil {
		b.parts = make(map[int][]byte)
	}
	b.parts[id] = data
	return fmt.Sprint("checksum-", id), err
}
func (b *resumeBackend) CreateSuperFile(_ string, _ string, _ string, _ int64, checksums map[int]string) error {
	b.merged++
	b.checksums = checksums
	return nil
}

func TestUploadAcceptsFreshAndCompleteResumePartitions(t *testing.T) {
	for _, mode := range []string{"fresh", "partial", "unordered", "complete"} {
		t.Run(mode, func(t *testing.T) {
			backend := &resumeBackend{parts: make(map[int][]byte)}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 2, BlockSize: 4}, "/synthetic")
			if snapshot := u.InstanceState(); len(snapshot.BlockList) != 0 || snapshot.Uploadid != "" {
				t.Fatal("unexpected initial snapshot")
			}
			wantUploads := 2
			if mode != "fresh" {
				blocks := []*BlockState{
					{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: "cached-0"},
					{ID: 1, Range: transfer.Range{Begin: 4, End: 8}},
				}
				backend.parts[0] = []byte("abcd")
				wantUploads = 1
				if mode == "complete" {
					blocks[1].CheckSum = "cached-1"
					backend.parts[1] = []byte("efgh")
					wantUploads = 0
				}
				if mode == "unordered" {
					blocks[0], blocks[1] = blocks[1], blocks[0]
				}
				u.SetInstanceState(&InstanceState{Uploadid: "synthetic", BlockList: blocks})
			}
			var uploadErr error
			success := false
			u.OnError(func(err error) { uploadErr = err })
			u.OnSuccess(func() { success = true })
			u.Execute()
			if uploadErr != nil || !success {
				t.Fatalf("valid upload rejected: %v", uploadErr)
			}
			if backend.uploaded != wantUploads || backend.merged != 1 || len(backend.checksums) != 2 {
				t.Fatalf("unexpected upload activity: uploaded=%d merged=%d checksums=%v", backend.uploaded, backend.merged, backend.checksums)
			}
			if string(append(append([]byte(nil), backend.parts[0]...), backend.parts[1]...)) != "abcdefgh" {
				t.Fatal("resumed blocks do not reconstruct the original file")
			}
			if len(u.InstanceState().BlockList) != 2 {
				t.Fatal("completed upload lost resume snapshot")
			}
		})
	}
}

func TestInvalidResumePartitionsNeverReachUploadBackend(t *testing.T) {
	block := func(id int, begin, end int64) *BlockState {
		return &BlockState{ID: id, Range: transfer.Range{Begin: begin, End: end}, CheckSum: "completed"}
	}
	for name, blocks := range map[string][]*BlockState{
		"nil_block":      {nil},
		"missing_prefix": {block(0, 4, 8)},
		"missing_tail":   {block(0, 0, 4)},
		"gap":            {block(0, 0, 3), block(1, 4, 8)},
		"overlap":        {block(0, 0, 5), block(1, 4, 8)},
		"duplicate_id":   {block(0, 0, 4), block(0, 4, 8)},
		"negative_id":    {block(-1, 0, 8)},
		"missing_id":     {block(1, 0, 8)},
		"out_of_bounds":  {block(0, 0, 9)},
		"empty_range":    {block(0, 0, 0), block(1, 0, 8)},
		"reversed_range": {block(0, 0, 5), block(1, 5, 4)},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &resumeBackend{}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/synthetic")
			u.SetInstanceState(&InstanceState{Uploadid: "synthetic", BlockList: blocks})
			var uploadErr error
			success, finished := false, false
			u.OnError(func(err error) { uploadErr = err })
			u.OnSuccess(func() { success = true })
			u.OnFinish(func() { finished = true })
			func() {
				defer func() {
					if value := recover(); value != nil {
						t.Errorf("invalid resume state panicked: %v", value)
					}
				}()
				u.Execute()
			}()
			if !errors.Is(uploadErr, ErrInvalidInstanceState) || success || !finished {
				t.Errorf("invalid state was not reported cleanly: err=%v success=%v finished=%v", uploadErr, success, finished)
			}
			if backend.precreated != 0 || backend.uploaded != 0 || backend.merged != 0 {
				t.Error("invalid resume state reached upload backend")
			}
		})
	}
}
