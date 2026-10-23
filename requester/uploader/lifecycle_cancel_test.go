package uploader

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type lifecycleCancelBackend struct {
	resumeBackend
	phase                      string
	returnNil                  bool
	started, canceled, release chan struct{}
}

func (b *lifecycleCancelBackend) wait(ctx context.Context) error {
	close(b.started)
	<-ctx.Done()
	close(b.canceled)
	<-b.release
	if b.returnNil {
		return nil
	}
	return ctx.Err()
}

func (b *lifecycleCancelBackend) Precreate(ctx context.Context) (string, pcserror.Error) {
	if b.phase != "precreate" {
		return b.resumeBackend.Precreate(ctx)
	}
	if err := b.wait(ctx); err != nil {
		info := pcserror.NewPCSErrorInfo("fixture precreate")
		info.SetNetError(err)
		return "", info
	}
	return "fixture", nil
}

func (b *lifecycleCancelBackend) CreateSuperFile(ctx context.Context, host, policy, id string, size int64, sums map[int]string) error {
	return b.wait(ctx)
}

func TestUploadCancellationCoversLifecycleStages(t *testing.T) {
	for _, phase := range []string{"precreate", "merge"} {
		for _, returnNil := range []bool{false, true} {
			name := phase
			if returnNil {
				name += "/backend_returns_success"
			}
			t.Run(name, func(t *testing.T) {
				backend := &lifecycleCancelBackend{phase: phase, returnNil: returnNil, started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
				u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("fixture"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 8}, "/fixture")
				success, canceled := false, false
				u.OnSuccess(func() { success = true })
				u.OnCancel(func() { canceled = true })
				done := make(chan struct{})
				go func() { u.Execute(); close(done) }()
				defer func() { close(backend.release); <-done }()
				select {
				case <-backend.started:
				case <-time.After(time.Second):
					t.Fatal("lifecycle operation did not start")
				}
				u.Cancel()
				select {
				case <-backend.canceled:
				case <-time.After(time.Second):
					t.Fatal("lifecycle operation did not receive cancellation")
				}
				select {
				case <-done:
					t.Fatal("returned before active operation exited")
				default:
				}
				// Release and join the backend before checking callbacks below.
				backend.release <- struct{}{}
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("canceled upload did not finish")
				}
				if success || !canceled {
					t.Errorf("cancel ignored: success=%v canceled=%v", success, canceled)
				}
				if phase == "precreate" && backend.uploaded != 0 {
					t.Error("dispatched parts after canceled precreate")
				}
			})
		}
	}
}
