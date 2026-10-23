package taskframework_test

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"sync/atomic"
	"testing"
	"time"
)

type callbackUnit struct {
	completedUnit
	run func()
}

func (u *callbackUnit) Run() *taskframework.TaskUnitRunResult {
	u.run()
	return &taskframework.TaskUnitRunResult{Succeed: true}
}

func TestStopWhileWaitingForParallelSlot(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(1)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var secondRuns int32
	executor.AppendNoRetry(&callbackUnit{run: func() { close(started); <-release }})
	executor.AppendNoRetry(&callbackUnit{run: func() { atomic.AddInt32(&secondRuns, 1) }})
	go func() { executor.Execute(); close(finished) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first task did not start")
	}
	deadline := time.Now().Add(time.Second)
	for executor.Count() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if executor.Count() != 0 {
		close(release)
		t.Fatal("executor did not reach slot wait")
	}
	executor.Stop()
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stopped executor did not finish")
	}
	if atomic.LoadInt32(&secondRuns) != 0 {
		t.Fatal("waiting task started after stop")
	}
	if executor.Count() != 1 {
		t.Fatal("unstarted task lost from queue")
	}
}
