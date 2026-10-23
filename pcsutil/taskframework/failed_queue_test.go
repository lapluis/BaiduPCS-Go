package taskframework_test

import (
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"testing"
)

type completedUnit struct {
	TestUnit
	success bool
}

func (u *completedUnit) Run() *taskframework.TaskUnitRunResult {
	return &taskframework.TaskUnitRunResult{Succeed: u.success}
}
func (u *completedUnit) OnFailed(*taskframework.TaskUnitRunResult)   {}
func (u *completedUnit) OnSuccess(*taskframework.TaskUnitRunResult)  {}
func (u *completedUnit) OnComplete(*taskframework.TaskUnitRunResult) {}

func TestFailedQueueSurvivesAppendingAndExecuting(t *testing.T) {
	executor := taskframework.NewTaskExecutor()
	executor.IsFailedDeque = true
	executor.AppendNoRetry(&completedUnit{})
	executor.Execute()
	failed := executor.FailedDeque()
	if failed == nil || failed.Size() != 1 {
		t.Fatal("initial failure not recorded")
	}
	executor.AppendNoRetry(&completedUnit{success: true})
	if executor.FailedDeque() != failed || failed.Size() != 1 {
		t.Fatal("appending task replaced failure history")
	}
	executor.Execute()
	if executor.FailedDeque() != failed || executor.FailedDeque().Size() != 1 {
		t.Fatal("execution replaced failure history")
	}
}
