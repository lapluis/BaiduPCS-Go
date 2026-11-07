package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
)

func TestMultiUploadDefaultsDoNotChangeCallerConfig(t *testing.T) {
	for _, input := range []MultiUploaderConfig{{}, {Parallel: -1, BlockSize: -2}, {Parallel: 1, BlockSize: 4, Policy: "fail"}} {
		t.Run(fmt.Sprintf("%d/%d", input.Parallel, input.BlockSize), func(t *testing.T) {
			cfg := input
			u := NewMultiUploader(nil, nil, &cfg, "/fixture")
			u.lazyInit()
			if cfg.Parallel != input.Parallel || cfg.BlockSize != input.BlockSize || cfg.Policy != input.Policy {
				t.Fatalf("defaults modified caller: %+v want=%+v", cfg, input)
			}
			parallel, block := input.Parallel, input.BlockSize
			if parallel <= 0 {
				parallel = 4
			}
			if block <= 0 {
				block = 16 * converter.MB
			}
			if u.config == &cfg || u.config.Parallel != parallel || u.config.BlockSize != block {
				t.Fatal("private defaults incorrect", u.config)
			}
		})
	}
}

type snapshotConfigBackend struct {
	resumeBackend
	policy string
}

func (b *snapshotConfigBackend) CreateSuperFile(ctx context.Context, host, policy, id string, size int64, sums map[int]string) error {
	b.policy = policy
	return b.resumeBackend.CreateSuperFile(ctx, host, policy, id, size, sums)
}

func TestMultiUploadKeepsExecutionConfigSnapshot(t *testing.T) {
	for _, changeValidator := range []bool{false, true} {
		t.Run(fmt.Sprint(changeValidator), func(t *testing.T) {
			testMultiUploadKeepsExecutionConfigSnapshot(t, changeValidator)
		})
	}
}

func testMultiUploadKeepsExecutionConfigSnapshot(t *testing.T, changeValidator bool) {
	t.Helper()
	var validated atomic.Int32
	cfg := &MultiUploaderConfig{Parallel: 1, BlockSize: 4, Policy: "fail", ValidateSource: func() error { validated.Add(1); return nil }}
	backend := &snapshotConfigBackend{}
	u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, cfg, "/fixture")
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	u.OnExecute(func() { close(started); <-release })
	done := make(chan error, 1)
	go func() { done <- u.ExecuteWithError() }()
	joined := false
	defer func() {
		unblock()
		if !joined {
			<-done
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upload did not reach startup callback")
	}
	// A caller can reuse its settings for the next job after this job starts.
	// Synchronization avoids racing the first job's initialization itself.
	cfg.Parallel = 2
	cfg.BlockSize = 8
	cfg.Policy = "overwrite"
	if changeValidator {
		cfg.ValidateSource = func() error { return errors.New("different job validator") }
	}
	unblock()
	err := <-done
	joined = true
	if err != nil || backend.policy != "fail" || backend.merged != 1 || backend.uploaded != 2 || validated.Load() < 2 {
		t.Fatalf("running config changed: err=%v policy=%q merge=%d parts=%d validations=%d", err, backend.policy, backend.merged, backend.uploaded, validated.Load())
	}
	if u.config.Parallel != 1 || u.config.BlockSize != 4 {
		t.Fatal("running concurrency or partition changed", u.config)
	}
	next := NewMultiUploader(nil, nil, cfg, "/next")
	next.lazyInit()
	if next.config.Policy != "overwrite" || next.config.Parallel != 2 || next.config.BlockSize != 8 {
		t.Fatal("next job did not receive updated settings", next.config)
	}
}
