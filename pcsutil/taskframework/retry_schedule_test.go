package taskframework_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

type scheduledRetryUnit struct {
	completedUnit
	waiting, retried                 chan struct{}
	delay                            time.Duration
	runs                             atomic.Int32
	successes, failures, completions atomic.Int32
}

func (u *scheduledRetryUnit) Run() *taskframework.TaskUnitRunResult {
	if u.runs.Add(1) == 1 {
		return &taskframework.TaskUnitRunResult{NeedRetry: true}
	}
	close(u.retried)
	return &taskframework.TaskUnitRunResult{Succeed: true}
}
func (u *scheduledRetryUnit) OnRetry(*taskframework.TaskUnitRunResult) { close(u.waiting) }
func (u *scheduledRetryUnit) RetryWait() time.Duration                 { return u.delay }

func (u *scheduledRetryUnit) OnSuccess(*taskframework.TaskUnitRunResult)  { u.successes.Add(1) }
func (u *scheduledRetryUnit) OnFailed(*taskframework.TaskUnitRunResult)   { u.failures.Add(1) }
func (u *scheduledRetryUnit) OnComplete(*taskframework.TaskUnitRunResult) { u.completions.Add(1) }

func TestMultipleDelayedRetriesPreserveTasksAndCallbacks(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(3)
	executor.IsFailedDeque = true
	var units []*scheduledRetryUnit
	var infos []*taskframework.TaskInfo
	for i := 0; i < 12; i++ {
		delay := []time.Duration{0, -time.Millisecond, 2 * time.Millisecond}[i%3]
		unit := &scheduledRetryUnit{waiting: make(chan struct{}), retried: make(chan struct{}), delay: delay}
		units = append(units, unit)
		infos = append(infos, executor.Append(unit, 1))
	}
	executor.Execute()
	for i, unit := range units {
		if unit.runs.Load() != 2 || unit.successes.Load() != 1 || unit.failures.Load() != 0 || unit.completions.Load() != 2 || infos[i].Retry() != 1 {
			t.Fatalf("retry task %d lost or duplicated: runs=%d success=%d failed=%d complete=%d retry=%d", i, unit.runs.Load(), unit.successes.Load(), unit.failures.Load(), unit.completions.Load(), infos[i].Retry())
		}
	}
	if executor.Count() != 0 || executor.FailedDeque().Size() != 0 {
		t.Fatal("successful retries remained pending or failed")
	}
}

func TestTaskGoroutineExitReleasesParallelSlot(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(1)
	executor.AppendNoRetry(&callbackUnit{run: runtime.Goexit})
	second, done := make(chan struct{}), make(chan struct{})
	executor.AppendNoRetry(&callbackUnit{run: func() { close(second) }})
	go func() { executor.Execute(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		executor.Stop()
		t.Fatal("exited task retained a running slot")
	}
	select {
	case <-second:
	default:
		t.Fatal("task after exited goroutine did not run")
	}
	if executor.Count() != 0 {
		t.Fatal("queued task lost after goroutine exit")
	}
}

func TestTaskSchedulingKeepsParallelBound(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(3)
	started, release, done := make(chan struct{}, 9), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseTasks := func() { releaseOnce.Do(func() { close(release) }) }
	var active, runs, exceeded atomic.Int32
	for i := 0; i < 9; i++ {
		executor.AppendNoRetry(&callbackUnit{run: func() {
			if active.Add(1) > 3 {
				exceeded.Add(1)
			}
			runs.Add(1)
			started <- struct{}{}
			<-release
			active.Add(-1)
		}})
	}
	go func() { executor.Execute(); close(done) }()
	defer func() {
		executor.Stop()
		releaseTasks()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("executor did not join tasks")
		}
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("available parallel slot was not used")
		}
	}
	select {
	case <-started:
		t.Fatal("scheduler exceeded parallel limit")
	case <-time.After(50 * time.Millisecond):
	}
	if executor.Count() != 6 {
		t.Fatalf("scheduler took work without an available slot: %d", executor.Count())
	}
	releaseTasks()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("executor did not finish all queued tasks")
	}
	if runs.Load() != 9 || active.Load() != 0 || exceeded.Load() != 0 || executor.Count() != 0 {
		t.Fatalf("parallel bound or completion changed: runs=%d active=%d exceeded=%d pending=%d", runs.Load(), active.Load(), exceeded.Load(), executor.Count())
	}
}

func TestRetryDelayDoesNotOccupyParallelSlot(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(1)
	first := &scheduledRetryUnit{waiting: make(chan struct{}), retried: make(chan struct{}), delay: time.Hour}
	executor.Append(first, 1)
	secondStarted, done := make(chan struct{}), make(chan struct{})
	executor.AppendNoRetry(&callbackUnit{run: func() { close(secondStarted) }})
	go func() { executor.Execute(); close(done) }()
	defer func() {
		executor.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("executor did not stop")
		}
	}()
	select {
	case <-first.waiting:
	case <-time.After(time.Second):
		t.Fatal("first task did not request retry")
	}
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("retry delay occupied the only slot and blocked a ready task")
	}
	executor.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop did not interrupt pending retry")
	}
	if first.runs.Load() != 1 || executor.Count() != 1 {
		t.Fatalf("stop lost or ran delayed retry: runs=%d pending=%d", first.runs.Load(), executor.Count())
	}
}

func TestReadyRetryRunsWhileAnotherTaskRemainsActive(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.SetParallel(2)
	first := &scheduledRetryUnit{waiting: make(chan struct{}), retried: make(chan struct{}), delay: 25 * time.Millisecond}
	info := executor.Append(first, 1)
	peerStarted, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releasePeer := func() { releaseOnce.Do(func() { close(release) }) }
	executor.AppendNoRetry(&callbackUnit{run: func() { close(peerStarted); <-release }})
	start := time.Now()
	go func() { executor.Execute(); close(done) }()
	defer func() {
		executor.Stop()
		releasePeer()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("executor did not join active peer")
		}
	}()
	select {
	case <-peerStarted:
	case <-time.After(time.Second):
		t.Fatal("peer did not start")
	}
	select {
	case <-first.retried:
	case <-time.After(time.Second):
		t.Fatal("ready retry waited for an unrelated active task")
	}
	if time.Since(start) < first.delay {
		t.Fatal("retry bypassed its delay")
	}
	releasePeer()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("executor did not finish")
	}
	if first.runs.Load() != 2 || info.Retry() != 1 || executor.Count() != 0 {
		t.Fatalf("retry count or pending queue changed: runs=%d retries=%d pending=%d", first.runs.Load(), info.Retry(), executor.Count())
	}
}
