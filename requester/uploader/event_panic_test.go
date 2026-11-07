package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUploadEventPanicDoesNotSkipFinalization(t *testing.T) {
	for _, phase := range []string{"execute", "execute-cancel", "execute-graceful", "success", "cancel", "error", "finish", "success-and-finish", "error-and-finish"} {
		for _, typed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", phase, typed), func(t *testing.T) {
				backend := &resumeBackend{}
				backendCause := errors.New("synthetic validation failure")
				config := &MultiUploaderConfig{Parallel: 1, BlockSize: 4}
				if phase == "error" || phase == "error-and-finish" {
					config.ValidateSource = func() error { return backendCause }
				}
				u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, config, "/fixture")
				cause := errors.New("synthetic upload event panic")
				finishCause := errors.New("synthetic finish event panic")
				raise := func() {
					if typed {
						panic(cause)
					}
					panic(cause.Error())
				}
				errorsSeen, finished := 0, 0
				u.OnExecute(func() {
					if phase == "execute-cancel" {
						u.Cancel()
					}
					if phase == "execute-graceful" {
						u.GracefulStop()
					}
					if strings.HasPrefix(phase, "execute") {
						raise()
					}
					if phase == "cancel" {
						u.Cancel()
					}
				})
				u.OnSuccess(func() {
					if phase == "success" || phase == "success-and-finish" {
						raise()
					}
				})
				u.OnCancel(func() {
					if phase == "cancel" {
						raise()
					}
				})
				u.OnError(func(err error) {
					errorsSeen++
					if phase == "error" || phase == "error-and-finish" {
						raise()
					}
				})
				u.OnFinish(func() {
					finished++
					if phase == "finish" || phase == "success-and-finish" || phase == "error-and-finish" {
						if typed {
							panic(finishCause)
						}
						panic(finishCause.Error())
					}
				})
				var escaped any
				var resultErr error
				func() { defer func() { escaped = recover() }(); resultErr = u.ExecuteWithError() }()
				if escaped != nil {
					t.Errorf("event panic escaped Execute: %v", escaped)
				}
				if finished != 1 || errorsSeen != 1 {
					t.Errorf("finalization skipped or error callback repeated: finish=%d errors=%d", finished, errorsSeen)
				}
				if !errors.Is(resultErr, ErrEventCallback) {
					t.Fatalf("event failure not returned: %v", resultErr)
				}
				if phase != "finish" && (!strings.Contains(resultErr.Error(), cause.Error()) || typed && !errors.Is(resultErr, cause)) {
					t.Fatalf("event panic cause lost: %v", resultErr)
				}
				if strings.Contains(phase, "finish") && (!strings.Contains(resultErr.Error(), finishCause.Error()) || typed && !errors.Is(resultErr, finishCause)) {
					t.Fatalf("finish panic cause lost: %v", resultErr)
				}
				if errors.Is(resultErr, backendCause) != (phase == "error" || phase == "error-and-finish") || errors.Is(resultErr, context.Canceled) != (phase == "cancel" || phase == "execute-cancel" || phase == "execute-graceful") {
					t.Fatalf("original transfer/cancel reason lost: %v", resultErr)
				}
				if strings.HasPrefix(phase, "execute") && (backend.precreated != 0 || backend.uploaded != 0 || backend.merged != 0) {
					t.Fatal("startup panic allowed network work")
				}
			})
		}
	}
}

func TestUploadExecuteWithErrorWithoutObserver(t *testing.T) {
	for _, phase := range []string{"healthy", "event", "source"} {
		t.Run(phase, func(t *testing.T) {
			backend := &resumeBackend{}
			cause := errors.New("synthetic unobserved upload failure")
			config := &MultiUploaderConfig{Parallel: 1, BlockSize: 4}
			if phase == "source" {
				config.ValidateSource = func() error { return cause }
			}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, config, "/fixture")
			if phase == "event" {
				u.OnExecute(func() { panic(cause) })
			}
			err := u.ExecuteWithError()
			if phase == "healthy" {
				if err != nil || backend.merged != 1 {
					t.Fatalf("healthy execution changed: %v", err)
				}
			} else if !errors.Is(err, cause) || errors.Is(err, ErrEventCallback) != (phase == "event") || backend.precreated != 0 {
				t.Fatalf("failure unavailable without observer: %v", err)
			}
			select {
			case <-u.finished:
			default:
				t.Fatal("execution did not finalize")
			}
		})
	}
}

func TestLegacyUploadExecuteRetainsProtectedEvents(t *testing.T) {
	backend := &resumeBackend{}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
	cause := errors.New("synthetic legacy execute panic")
	u.OnExecute(func() { panic(cause) })
	var observed error
	finished := false
	u.OnError(func(err error) { observed = err })
	u.OnFinish(func() { finished = true })
	u.Execute()
	if !errors.Is(observed, cause) || !finished || backend.precreated != 0 {
		t.Fatalf("legacy Execute compatibility lost: %v finish=%t precreate=%d", observed, finished, backend.precreated)
	}
}
