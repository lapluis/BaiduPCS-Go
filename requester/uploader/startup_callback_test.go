package uploader

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type startupCallbackBackend struct {
	resumeBackend
	precreateStarted chan struct{}
}

func (b *startupCallbackBackend) Precreate(ctx context.Context) (string, pcserror.Error) {
	close(b.precreateStarted)
	return b.resumeBackend.Precreate(ctx)
}

func TestUploadStartupCallbackFinishesBeforeNetworkAndFinalEvents(t *testing.T) {
	for _, stop := range []string{"continue", "cancel", "graceful"} {
		t.Run(stop, func(t *testing.T) {
			backend := &startupCallbackBackend{precreateStarted: make(chan struct{})}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			started, release, callbackDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			defer unblock()
			u.OnExecute(func() {
				defer close(callbackDone)
				close(started)
				<-release
				if stop == "cancel" {
					u.Cancel()
				} else if stop == "graceful" {
					u.GracefulStop()
				}
			})
			var succeeded, canceled, finished atomic.Bool
			u.OnSuccess(func() { succeeded.Store(true) })
			u.OnCancel(func() { canceled.Store(true) })
			u.OnFinish(func() { finished.Store(true) })
			done := make(chan struct{})
			go func() { u.Execute(); close(done) }()
			<-started
			select {
			case <-backend.precreateStarted:
				t.Error("network operation started before startup callback returned")
			case <-time.After(50 * time.Millisecond):
			}
			if succeeded.Load() || canceled.Load() || finished.Load() {
				t.Error("final event delivered while startup callback was still active")
			}
			unblock()
			<-callbackDone
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("upload did not finish after startup callback returned")
			}
			if stop == "continue" {
				if !succeeded.Load() || canceled.Load() || !finished.Load() || backend.precreated != 1 || backend.uploaded != 2 || backend.merged != 1 {
					t.Fatalf("normal upload lifecycle changed: success=%v canceled=%v finished=%v precreate=%d parts=%d merge=%d", succeeded.Load(), canceled.Load(), finished.Load(), backend.precreated, backend.uploaded, backend.merged)
				}
				return
			}
			if succeeded.Load() || !canceled.Load() || !finished.Load() || backend.precreated != 0 || backend.uploaded != 0 || backend.merged != 0 {
				t.Fatalf("startup stop ignored: success=%v canceled=%v finished=%v precreate=%d parts=%d merge=%d", succeeded.Load(), canceled.Load(), finished.Load(), backend.precreated, backend.uploaded, backend.merged)
			}
		})
	}
}
