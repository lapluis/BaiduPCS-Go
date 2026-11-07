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

type waitingRetryUnit struct {
	completedUnit
	waiting chan struct{}
}

func (u *waitingRetryUnit) Run() *taskframework.TaskUnitRunResult {
	return &taskframework.TaskUnitRunResult{NeedRetry: true}
}
func (u *waitingRetryUnit) OnRetry(*taskframework.TaskUnitRunResult) { close(u.waiting) }
func (u *waitingRetryUnit) RetryWait() time.Duration                 { return time.Hour }

func TestStopInterruptsRetryDelay(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	waiting, finished := make(chan struct{}), make(chan struct{})
	executor.Append(&waitingRetryUnit{waiting: waiting}, 1)
	go func() { executor.Execute(); close(finished) }()
	select {
	case <-waiting:
	case <-time.After(time.Second):
		t.Fatal("retry did not start")
	}
	executor.Stop()
	executor.Stop()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stop did not interrupt retry delay")
	}
	if executor.Count() != 1 {
		t.Fatal("pending retry lost")
	}
}

func (u *callbackUnit) Run() *taskframework.TaskUnitRunResult {
	u.run()
	return &taskframework.TaskUnitRunResult{Succeed: true}
}

func TestStopWithAllParallelSlotsBusy(t *testing.T) {
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
	// The scheduler leaves work queued until a slot is available.
	if executor.Count() != 1 {
		close(release)
		t.Fatal("executor dequeued a task without an available slot")
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
