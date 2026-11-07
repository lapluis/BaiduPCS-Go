package taskframework_test

import (
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
)

type successfulResultUnit struct {
	TestUnit
	successes, failures, completions int
	completed                        *taskframework.TaskUnitRunResult
}

func (u *successfulResultUnit) Run() *taskframework.TaskUnitRunResult {
	return taskframework.TaskUnitRunResultSuccess
}
func (u *successfulResultUnit) OnSuccess(*taskframework.TaskUnitRunResult) { u.successes++ }
func (u *successfulResultUnit) OnFailed(*taskframework.TaskUnitRunResult)  { u.failures++ }
func (u *successfulResultUnit) OnComplete(result *taskframework.TaskUnitRunResult) {
	u.completions++
	u.completed = result
}

func TestSuccessfulResultUsesSuccessLifecycleAndAvoidsFailedQueue(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.IsFailedDeque = true
	unit := &successfulResultUnit{}
	executor.AppendNoRetry(unit)
	executor.Execute()
	if unit.successes != 1 || unit.failures != 0 || unit.completions != 1 || unit.completed != taskframework.TaskUnitRunResultSuccess {
		t.Fatalf("predefined success ran wrong lifecycle: successes=%d failures=%d completions=%d result=%+v", unit.successes, unit.failures, unit.completions, unit.completed)
	}
	if executor.Count() != 0 || executor.FailedDeque() == nil || executor.FailedDeque().Size() != 0 {
		t.Fatalf("successful task remained pending or failed: pending=%d failed=%v", executor.Count(), executor.FailedDeque())
	}
}
