package uploader

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

func TestPartRetriesStopAtLimitAndPreserveCompletedState(t *testing.T) {
	failure := errors.New("synthetic persistent upload failure")
	guard := errors.New("retry limit was bypassed")
	calls := make(map[int]int)
	backend := &lifecycleBackend{part: func(_ context.Context, id int, r rio.ReaderLen64) (string, error) {
		calls[id]++
		if id == 0 {
			_, err := io.ReadAll(r)
			return "completed", err
		}
		if calls[id] > 3 {
			return "", &MultiError{Terminated: true, Err: guard}
		}
		return "", failure
	}}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	started := time.Now()
	err := u.ExecuteWithError()
	if calls[0] != 1 || calls[1] != 3 || backend.merged != 0 || !errors.Is(err, failure) || errors.Is(err, guard) || !errors.Is(err, ErrPartRetryExhausted) {
		t.Fatalf("persistent error retried indefinitely or lost: calls=%v merged=%d err=%v", calls, backend.merged, err)
	}
	if time.Since(started) < 3*time.Second {
		t.Fatal("failed requests retried without backoff")
	}
	state := u.InstanceState()
	if state.BlockList[0].CheckSum != "completed" || state.BlockList[1].CheckSum != "" {
		t.Fatalf("completed or pending state changed: %+v", state)
	}
	retryBackend := &resumeBackend{}
	retry := NewMultiUploader(retryBackend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	retry.SetInstanceState(state)
	if err := retry.ExecuteWithError(); err != nil || retryBackend.uploaded != 1 || retryBackend.merged != 1 {
		t.Fatalf("resume resent completed data or failed: uploads=%d merges=%d err=%v", retryBackend.uploaded, retryBackend.merged, err)
	}
}

func TestPartRetryRewindsPartBeforeRecovery(t *testing.T) {
	calls := 0
	backend := &lifecycleBackend{part: func(_ context.Context, _ int, r rio.ReaderLen64) (string, error) {
		calls++
		if calls == 1 {
			data := make([]byte, 1)
			if _, err := r.Read(data); err != nil {
				return "", &MultiError{Terminated: true, Err: err}
			}
			return "", errors.New("synthetic transient failure")
		}
		data, err := io.ReadAll(r)
		if string(data) != "abcd" {
			return "", &MultiError{Terminated: true, Err: errors.New("retry did not rewind part")}
		}
		return "completed", err
	}}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcd"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	if err := u.ExecuteWithError(); err != nil || calls != 2 || backend.merged != 1 {
		t.Fatalf("transient retry failed: calls=%d merges=%d err=%v", calls, backend.merged, err)
	}
}

func TestPartRetryBackoffStopsPromptly(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		name := "cancel"
		if graceful {
			name = "graceful-stop"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{})
			var calls atomic.Int32
			backend := &lifecycleBackend{part: func(context.Context, int, rio.ReaderLen64) (string, error) {
				if calls.Add(1) == 1 {
					close(started)
				}
				return "", errors.New("synthetic retryable failure")
			}}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcd"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			done := make(chan error, 1)
			go func() { done <- u.ExecuteWithError() }()
			defer u.Cancel()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("first request did not start")
			}
			select {
			case err := <-done:
				t.Fatalf("backoff did not wait: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if graceful {
				u.GracefulStop()
			} else {
				u.Cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrPartRetryExhausted) || calls.Load() != 1 || backend.merged != 0 {
					t.Fatalf("backoff dispatched after stop: calls=%d merges=%d err=%v", calls.Load(), backend.merged, err)
				}
			case <-time.After(500 * time.Millisecond):
				t.Fatal("stop waited for retry timer")
			}
		})
	}
}

func TestPartRetryLimitWaitsForActivePeer(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releasePeer := func() { releaseOnce.Do(func() { close(release) }) }
	defer releasePeer()
	failure := errors.New("synthetic persistent upload failure")
	backend := &lifecycleBackend{part: func(ctx context.Context, id int, _ rio.ReaderLen64) (string, error) {
		if id == 0 {
			<-started
			return "", failure
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return "", ctx.Err()
	}}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 2, BlockSize: 4}, "/fixture")
	defer u.Cancel()
	done := make(chan error, 1)
	go func() { done <- u.ExecuteWithError() }()
	select {
	case <-canceled:
	case <-time.After(6 * time.Second):
		t.Fatal("retry exhaustion did not cancel active peer")
	}
	select {
	case err := <-done:
		t.Fatalf("returned before active peer stopped: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releasePeer()
	select {
	case err := <-done:
		if !errors.Is(err, ErrPartRetryExhausted) || !errors.Is(err, failure) || errors.Is(err, context.Canceled) || backend.merged != 0 {
			t.Fatalf("peer cancellation replaced retry failure: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload did not join released peer")
	}
}
