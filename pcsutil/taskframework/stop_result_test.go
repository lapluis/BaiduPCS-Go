package taskframework_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

type stoppedResultUnit struct {
	completedUnit
	started, release                                       chan struct{}
	outcome                                                *taskframework.TaskUnitRunResult
	runs, retries, waits, successes, failures, completions atomic.Int32
}

func (u *stoppedResultUnit) Run() *taskframework.TaskUnitRunResult {
	u.runs.Add(1)
	u.started <- struct{}{}
	<-u.release
	return u.outcome
}

func (u *stoppedResultUnit) OnRetry(*taskframework.TaskUnitRunResult) { u.retries.Add(1) }
func (u *stoppedResultUnit) RetryWait() time.Duration {
	u.waits.Add(1)
	return time.Hour
}
func (u *stoppedResultUnit) OnSuccess(*taskframework.TaskUnitRunResult)  { u.successes.Add(1) }
func (u *stoppedResultUnit) OnFailed(*taskframework.TaskUnitRunResult)   { u.failures.Add(1) }
func (u *stoppedResultUnit) OnComplete(*taskframework.TaskUnitRunResult) { u.completions.Add(1) }

func TestStopPreservesActiveRetryResultsAndFinalOutcomes(t *testing.T) {
	for _, mode := range []string{"retry", "exhausted", "success", "failure"} {
		t.Run(mode, func(t *testing.T) {
			executor := taskframework.NewTaskExecutor()
			executor.SetParallel(3)
			executor.IsFailedDeque = true
			started, release, done := make(chan struct{}, 3), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			var units []*stoppedResultUnit
			var infos []*taskframework.TaskInfo
			for i := 0; i < 3; i++ {
				unit := &stoppedResultUnit{started: started, release: release, outcome: &taskframework.TaskUnitRunResult{Succeed: mode == "success", NeedRetry: mode == "retry" || mode == "exhausted"}}
				budget := 1
				if mode == "exhausted" {
					budget = 0
				}
				units = append(units, unit)
				infos = append(infos, executor.Append(unit, budget))
			}
			var queuedRuns atomic.Int32
			for i := 0; i < 2; i++ {
				executor.AppendNoRetry(&callbackUnit{run: func() { queuedRuns.Add(1) }})
			}
			go func() { executor.Execute(); close(done) }()
			defer func() { executor.Stop(); unblock(); <-done }()
			for i := 0; i < 3; i++ {
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("active tasks did not start")
				}
			}
			executor.Stop()
			executor.Stop()
			select {
			case <-done:
				t.Fatal("stop returned before active tasks completed")
			default:
			}
			unblock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("stop waited for retry delay or lost worker completion")
			}
			wantPending, wantFailures := 2, int32(0)
			if mode == "retry" {
				wantPending += 3
			}
			if mode == "exhausted" || mode == "failure" {
				wantFailures = 3
			}
			if executor.Count() != wantPending || executor.FailedDeque().Size() != int(wantFailures) || queuedRuns.Load() != 0 {
				t.Errorf("stop lost or dispatched work: pending=%d want=%d failed=%d want=%d queued-runs=%d", executor.Count(), wantPending, executor.FailedDeque().Size(), wantFailures, queuedRuns.Load())
			}
			for i, unit := range units {
				wantSuccess, wantFailure := int32(0), int32(0)
				if mode == "success" {
					wantSuccess = 1
				}
				if wantFailures != 0 {
					wantFailure = 1
				}
				if unit.runs.Load() != 1 || unit.retries.Load() != 0 || unit.waits.Load() != 0 || unit.completions.Load() != 1 || unit.successes.Load() != wantSuccess || unit.failures.Load() != wantFailure || infos[i].Retry() != 0 {
					t.Errorf("task %d outcome changed on stop: run=%d retry=%d wait=%d complete=%d success=%d failure=%d budget-used=%d", i, unit.runs.Load(), unit.retries.Load(), unit.waits.Load(), unit.completions.Load(), unit.successes.Load(), unit.failures.Load(), infos[i].Retry())
				}
			}
			executor.Execute()
			if executor.Count() != wantPending || executor.FailedDeque().Size() != int(wantFailures) || queuedRuns.Load() != 0 {
				t.Fatal("executing a stopped executor consumed retained tasks")
			}
		})
	}
}
