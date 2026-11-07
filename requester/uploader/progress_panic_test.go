package uploader

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type uploadProgressPanicBackend struct {
	resumeBackend
	phase            string
	release, stopped chan struct{}
	stopOnce         sync.Once
	cause            error
}

func (b *uploadProgressPanicBackend) Precreate(ctx context.Context) (string, pcserror.Error) {
	if b.phase != "precreate" {
		return b.resumeBackend.Precreate(ctx)
	}
	<-ctx.Done()
	b.stopOnce.Do(func() { close(b.stopped) })
	err := pcserror.NewPCSErrorInfo("synthetic precreate")
	err.SetNetError(ctx.Err())
	return "", err
}
func (b *uploadProgressPanicBackend) TmpFile(ctx context.Context, id, target string, part int, offset int64, r rio.ReaderLen64) (string, error) {
	if part == 1 {
		select {
		case <-ctx.Done():
			b.stopOnce.Do(func() { close(b.stopped) })
			return "", ctx.Err()
		case <-b.release:
		}
		b.stopOnce.Do(func() { close(b.stopped) })
		if b.phase == "backend-error" {
			return "", &MultiError{Err: b.cause, Terminated: true}
		}
	}
	return b.resumeBackend.TmpFile(ctx, id, target, part, offset, r)
}

func TestUploadProgressPanicStopsAndPreservesParts(t *testing.T) {
	for _, phase := range []string{"typed", "string", "canceled", "late", "precreate", "backend-error"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUploadProgressPanicProcessFixture$")
			cmd.Env = append(os.Environ(), "PCS_UPLOAD_PROGRESS_PANIC="+phase)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("upload progress panic killed or failed child: %v %s", err, output)
			}
		})
	}
}

func TestUploadProgressPanicProcessFixture(t *testing.T) {
	phase := os.Getenv("PCS_UPLOAD_PROGRESS_PANIC")
	if phase == "" {
		return
	}
	backendCause := errors.New("synthetic concurrent upload failure")
	backend := &uploadProgressPanicBackend{phase: phase, release: make(chan struct{}), stopped: make(chan struct{}), cause: backendCause}
	release := sync.OnceFunc(func() { close(backend.release) })
	defer release()
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	u.SetInstanceState(&InstanceState{Uploadid: "synthetic-session"})
	cause := errors.New("synthetic upload progress panic")
	exited := make(chan struct{})
	var calls atomic.Int32
	u.OnUploadStatusEvent(func(Status, <-chan struct{}) {
		calls.Add(1)
		defer close(exited)
		if phase == "late" || phase == "backend-error" {
			release()
			<-u.finished
		}
		if phase == "canceled" {
			u.Cancel()
		}
		if phase == "string" {
			panic(cause.Error())
		}
		panic(cause)
	})
	var got error
	success, canceled, finished := false, false, false
	u.OnSuccess(func() { success = true })
	u.OnCancel(func() { canceled = true })
	u.OnError(func(err error) { got = err })
	u.OnFinish(func() {
		finished = true
		select {
		case <-exited:
		default:
			t.Error("finish preceded progress callback exit")
		}
	})
	u.Execute()
	if got == nil || !strings.Contains(got.Error(), cause.Error()) || (phase != "string" && !errors.Is(got, cause)) {
		t.Fatalf("upload progress error lost: %v", got)
	}
	if !errors.Is(got, ErrProgressCallback) {
		t.Errorf("progress failure classification lost: %v", got)
	}
	if errors.Is(got, context.Canceled) != (phase == "canceled") {
		t.Errorf("progress failure disguised as user cancellation: %v", got)
	}
	if errors.Is(got, backendCause) != (phase == "backend-error") {
		t.Errorf("concurrent upload error lost: %v", got)
	}
	if success || canceled || !finished || calls.Load() != 1 {
		t.Errorf("wrong completion events: success=%t cancel=%t finish=%t calls=%d", success, canceled, finished, calls.Load())
	}
	select {
	case <-backend.stopped:
	default:
		t.Fatal("active backend was not joined")
	}
	if backend.merged != 0 && phase != "late" {
		t.Fatal("merged after progress failure")
	}
	saved := u.InstanceState()
	if saved.Uploadid != "synthetic-session" || len(saved.BlockList) != 2 {
		t.Fatalf("upload session lost: %+v", saved)
	}
	if phase != "precreate" && saved.BlockList[0].CheckSum == "" {
		t.Fatal("completed part lost")
	}
	if phase != "late" && saved.BlockList[1].CheckSum != "" {
		t.Fatal("failed part marked complete")
	}
	retryBackend := &resumeBackend{}
	retry := NewMultiUploader(retryBackend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	retry.SetInstanceState(saved)
	var retryErr error
	retrySuccess := false
	retry.OnError(func(err error) { retryErr = err })
	retry.OnSuccess(func() { retrySuccess = true })
	retry.Execute()
	expected := 1
	if phase == "late" {
		expected = 0
	}
	if phase == "precreate" {
		expected = 2
	}
	if retryErr != nil || !retrySuccess || retryBackend.uploaded != expected || retryBackend.merged != 1 {
		t.Fatalf("resume failed or resent completed parts: err=%v success=%t uploaded=%d want=%d merged=%d", retryErr, retrySuccess, retryBackend.uploaded, expected, retryBackend.merged)
	}
}
