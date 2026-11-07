package taskframework

import "time"

type delayedTask struct {
	task  *TaskInfoItem
	ready time.Time
}

// One scheduler-owned heap and timer replace sleeping worker goroutines.
type delayedTaskQueue []*delayedTask

func (q delayedTaskQueue) Len() int                { return len(q) }
func (q delayedTaskQueue) Less(i, j int) bool      { return q[i].ready.Before(q[j].ready) }
func (q delayedTaskQueue) Swap(i, j int)           { q[i], q[j] = q[j], q[i] }
func (q *delayedTaskQueue) Push(value interface{}) { *q = append(*q, value.(*delayedTask)) }
func (q *delayedTaskQueue) Pop() interface{} {
	last := len(*q) - 1
	value := (*q)[last]
	(*q)[last] = nil
	*q = (*q)[:last]
	return value
}

// executeTask retains the task callbacks and retry budget, but returns delayed
// work to the scheduler instead of sleeping while holding a parallel slot.
func (te *TaskExecutor) executeTask(task *TaskInfoItem) *delayedTask {
	result := task.Unit.Run()
	if result == nil {
		task.Unit.OnComplete(result)
		return nil
	}
	if result.Succeed {
		task.Unit.OnSuccess(result)
		task.Unit.OnComplete(result)
		return nil
	}
	if result.NeedRetry {
		if task.Info.IsExceedRetry() {
			task.Unit.OnFailed(result)
			if te.IsFailedDeque {
				te.failedDeque.Append(task)
			}
			task.Unit.OnComplete(result)
			return nil
		}
		if te.Stopped() {
			// Keep unfinished work without consuming another retry or
			// invoking retry callbacks after the executor has been stopped.
			task.Unit.OnComplete(result)
			return &delayedTask{task: task}
		}
		task.Info.retry++
		task.Unit.OnRetry(result)
		task.Unit.OnComplete(result)
		return &delayedTask{task: task, ready: time.Now().Add(task.Unit.RetryWait())}
	}
	task.Unit.OnFailed(result)
	if te.IsFailedDeque && result.Extra != "skip" {
		te.failedDeque.Append(task)
	}
	task.Unit.OnComplete(result)
	return nil
}
