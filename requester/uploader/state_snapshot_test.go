package uploader

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestUploadStateInputIsCopied(t *testing.T) {
	state := &InstanceState{Uploadid: "original", BlockList: []*BlockState{{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: "cached"}, nil}}
	u := NewMultiUploader(nil, nil, nil, "/fixture")
	u.SetInstanceState(state)
	state.Uploadid = "other"
	state.BlockList[0].Range.End = 3
	state.BlockList[0].CheckSum = "other-checksum"
	state.BlockList[1] = &BlockState{ID: 1}
	if u.instanceState == state || u.instanceState.Uploadid != "original" || len(u.instanceState.BlockList) != 2 || u.instanceState.BlockList[0].Range.End != 4 || u.instanceState.BlockList[0].CheckSum != "cached" || u.instanceState.BlockList[1] != nil {
		t.Fatal("resume input aliases caller state", u.instanceState)
	}
	u.instanceState.BlockList[0].CheckSum = "private"
	if state.BlockList[0].CheckSum != "other-checksum" {
		t.Fatal("private state changed caller block")
	}
	u.SetInstanceState(nil)
	if u.instanceState != nil {
		t.Fatal("nil did not clear pre-start state")
	}
}

type sessionSnapshotBackend struct {
	resumeBackend
	partSessions []string
	mergeSession string
}

func (b *sessionSnapshotBackend) TmpFile(ctx context.Context, id, target string, part int, offset int64, r rio.ReaderLen64) (string, error) {
	b.partSessions = append(b.partSessions, id)
	return b.resumeBackend.TmpFile(ctx, id, target, part, offset, r)
}

func (b *sessionSnapshotBackend) CreateSuperFile(ctx context.Context, host, policy, id string, size int64, sums map[int]string) error {
	b.mergeSession = id
	return b.resumeBackend.CreateSuperFile(ctx, host, policy, id, size, sums)
}

func TestRunningUploadKeepsResumeSession(t *testing.T) {
	for _, mode := range []string{"input-mutation", "setter", "nil-setter"} {
		t.Run(mode, func(t *testing.T) {
			state := &InstanceState{Uploadid: "original-session", BlockList: []*BlockState{{ID: 0, Range: transfer.Range{Begin: 0, End: 4}, CheckSum: "cached-0"}, {ID: 1, Range: transfer.Range{Begin: 4, End: 8}}}}
			backend := &sessionSnapshotBackend{resumeBackend: resumeBackend{parts: map[int][]byte{0: []byte("abcd")}}}
			u := NewMultiUploader(backend, resumeReader{bytes.NewReader([]byte("abcdefgh"))}, &MultiUploaderConfig{Parallel: 1, BlockSize: 4}, "/fixture")
			u.SetInstanceState(state)
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
			if mode == "input-mutation" {
				state.Uploadid = "other-session"
			} else if mode == "nil-setter" {
				u.SetInstanceState(nil)
			} else {
				u.SetInstanceState(&InstanceState{Uploadid: "other-session"})
			}
			unblock()
			err := <-done
			joined = true
			if err != nil || fmt.Sprint(backend.partSessions) != "[original-session]" || backend.mergeSession != "original-session" || backend.uploaded != 1 || backend.merged != 1 || u.InstanceState().Uploadid != "original-session" {
				t.Fatalf("session changed after workers were built: err=%v parts=%v merge=%q counts=%d/%d state=%+v", err, backend.partSessions, backend.mergeSession, backend.uploaded, backend.merged, u.InstanceState())
			}
			if string(append(append([]byte(nil), backend.parts[0]...), backend.parts[1]...)) != "abcdefgh" {
				t.Fatal("resume bytes changed")
			}
		})
	}
}
