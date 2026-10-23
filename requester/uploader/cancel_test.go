package uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type cancelBackend struct {
	precreated, uploaded, merged atomic.Int32
	started, canceled, release   chan struct{}
}

func (b *cancelBackend) Precreate(context.Context) (string, pcserror.Error) {
	b.precreated.Add(1)
	return "synthetic", nil
}
func (b *cancelBackend) TmpFile(ctx context.Context, _ string, _ string, id int, _ int64, _ rio.ReaderLen64) (string, error) {
	b.uploaded.Add(1)
	if id != 0 {
		return "synthetic", nil
	}
	close(b.started)
	<-ctx.Done()
	close(b.canceled)
	<-b.release
	return "", ctx.Err()
}
func (b *cancelBackend) CreateSuperFile(_ context.Context, _ string, _ string, _ string, _ int64, _ map[int]string) error {
	b.merged.Add(1)
	return nil
}

func TestUploadCancelWaitsForActiveRequestAndStopsDispatch(t *testing.T) {
	backend := &cancelBackend{started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/synthetic")
	var canceled, success atomic.Bool
	u.OnCancel(func() { canceled.Store(true) })
	u.OnSuccess(func() { success.Store(true) })
	done := make(chan struct{})
	go func() { u.Execute(); close(done) }()
	select {
	case <-backend.started:
	case <-time.After(3 * time.Second):
		t.Fatal("upload did not start")
	}
	u.Cancel()
	select {
	case <-backend.canceled:
	case <-time.After(time.Second):
		t.Fatal("active request did not receive cancellation")
	}
	returnedEarly := false
	select {
	case <-done:
		returnedEarly = true
		t.Error("upload returned while backend still accessed the part")
	case <-time.After(100 * time.Millisecond):
	}
	close(backend.release)
	if !returnedEarly {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("upload did not finish after request stopped")
		}
	}
	if !canceled.Load() || success.Load() || backend.merged.Load() != 0 {
		t.Fatal("canceled upload reported success or merged file")
	}
	if got := backend.uploaded.Load(); got != 1 {
		t.Fatalf("cancellation dispatched additional parts: %d calls", got)
	}
}

func TestCanceledUploadDoesNotStartBackend(t *testing.T) {
	backend := &cancelBackend{}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/synthetic")
	u.Cancel()
	u.Execute()
	if backend.precreated.Load() != 0 || backend.uploaded.Load() != 0 || backend.merged.Load() != 0 {
		t.Fatal("already canceled uploader contacted backend")
	}
}

type lifecycleBackend struct {
	resumeBackend
	part func(context.Context, int, rio.ReaderLen64) (string, error)
}

func (b *lifecycleBackend) TmpFile(ctx context.Context, _ string, _ string, id int, _ int64, r rio.ReaderLen64) (string, error) {
	return b.part(ctx, id, r)
}

func TestFatalUploadErrorWaitsForCanceledPeer(t *testing.T) {
	peerStarted, peerCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	failure := errors.New("synthetic terminal failure")
	backend := &lifecycleBackend{part: func(ctx context.Context, id int, _ rio.ReaderLen64) (string, error) {
		if id == 0 {
			<-peerStarted
			return "", &MultiError{Err: failure, Terminated: true}
		}
		close(peerStarted)
		<-ctx.Done()
		close(peerCanceled)
		<-release
		return "", ctx.Err()
	}}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 2, BlockSize: 4}, "/synthetic")
	var uploadErr error
	u.OnError(func(err error) { uploadErr = err })
	done := make(chan struct{})
	go func() { u.Execute(); close(done) }()
	select {
	case <-peerCanceled:
	case <-time.After(3 * time.Second):
		t.Fatal("fatal upload error did not cancel peer")
	}
	returnedEarly := false
	select {
	case <-done:
		returnedEarly = true
		t.Error("fatal upload returned before peer exited")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if !returnedEarly {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("fatal upload did not return")
		}
	}
	if !errors.Is(uploadErr, failure) || backend.merged != 0 {
		t.Fatalf("fatal error lost or file merged: %v", uploadErr)
	}
}

func TestGracefulStopFinishesActivePartWithoutDispatchingNext(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	backend := &lifecycleBackend{part: func(_ context.Context, _ int, r rio.ReaderLen64) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		_, err := io.ReadAll(r)
		return "completed", err
	}}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/synthetic")
	var canceled atomic.Bool
	u.OnCancel(func() { canceled.Store(true) })
	done := make(chan struct{})
	go func() { u.Execute(); close(done) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("part did not start")
	}
	u.GracefulStop()
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("graceful upload stop did not finish")
	}
	state := u.InstanceState()
	if !canceled.Load() || calls.Load() != 1 || backend.merged != 0 || len(state.BlockList) != 2 || state.BlockList[0].CheckSum == "" || state.BlockList[1].CheckSum != "" {
		t.Fatalf("graceful stop lost completed progress or dispatched extra parts: calls=%d state=%+v", calls.Load(), state)
	}
}
