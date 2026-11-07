package taskframework

import (
	"container/heap"
	"github.com/GeertJohan/go.incremental"
	"github.com/oleiade/lane"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type (
	TaskExecutor struct {
		incr      *incremental.Int // 任务id生成
		deque     *lane.Deque      // 队列
		parallel  int              // 任务的最大并发量
		stopped   int32            // 停止标志, 置位后不再派发新任务
		stopInit  sync.Once
		stopClose sync.Once
		stopChan  chan struct{}

		// 是否统计失败队列
		IsFailedDeque bool
		failedDeque   *lane.Deque
	}
)

func NewTaskExecutor() *TaskExecutor {
	return &TaskExecutor{}
}

func (te *TaskExecutor) lazyInit() {
	if te.deque == nil {
		te.deque = lane.NewDeque()
	}
	if te.incr == nil {
		te.incr = &incremental.Int{}
	}
	if te.parallel < 1 {
		te.parallel = 1
	}
	if te.IsFailedDeque && te.failedDeque == nil {
		te.failedDeque = lane.NewDeque()
	}
}

// 设置任务的最大并发量
func (te *TaskExecutor) SetParallel(parallel int) {
	te.parallel = parallel
}

// Append 将任务加到任务队列末尾
func (te *TaskExecutor) Append(unit TaskUnit, maxRetry int) *TaskInfo {
	te.lazyInit()
	taskInfo := &TaskInfo{
		id:       strconv.Itoa(te.incr.Next()),
		maxRetry: maxRetry,
	}
	unit.SetTaskInfo(taskInfo)
	te.deque.Append(&TaskInfoItem{
		Info: taskInfo,
		Unit: unit,
	})
	return taskInfo
}

// AppendNoRetry 将任务加到任务队列末尾, 不重试
func (te *TaskExecutor) AppendNoRetry(unit TaskUnit) {
	te.Append(unit, 0)
}

// Count 返回任务数量
func (te *TaskExecutor) Count() int {
	if te.deque == nil {
		return 0
	}
	return te.deque.Size()
}

// Execute 执行任务
func (te *TaskExecutor) Execute() {
	te.lazyInit()
	completed := make(chan *delayedTask)
	var running sync.WaitGroup
	defer running.Wait()
	var pending delayedTaskQueue
	active := 0
	for {
		stopped := te.Stopped()
		if stopped {
			// Preserve delayed retries without waiting for their deadlines.
			for pending.Len() != 0 {
				te.deque.Append(heap.Pop(&pending).(*delayedTask).task)
			}
		} else {
			now := time.Now()
			for pending.Len() != 0 && !pending[0].ready.After(now) {
				te.deque.Append(heap.Pop(&pending).(*delayedTask).task)
			}
			for active < te.parallel && !te.Stopped() {
				e := te.deque.Shift()
				if e == nil {
					break
				}
				task := e.(*TaskInfoItem)
				if te.Stopped() {
					te.deque.Prepend(task)
					break
				}
				active++
				running.Add(1)
				go func() {
					defer running.Done()
					var retry *delayedTask
					defer func() { completed <- retry }()
					retry = te.executeTask(task)
				}()
			}
		}
		if active == 0 && pending.Len() == 0 && (stopped || te.deque.Size() == 0) {
			return
		}
		var timer *time.Timer
		var ready <-chan time.Time
		if pending.Len() != 0 {
			timer = time.NewTimer(time.Until(pending[0].ready))
			ready = timer.C
		}
		var stop <-chan struct{}
		if !stopped {
			stop = te.stopSignal()
		}
		select {
		case retry := <-completed:
			active--
			if retry != nil {
				heap.Push(&pending, retry)
			}
		case <-ready:
		case <-stop:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// FailedDeque 获取失败队列
func (te *TaskExecutor) FailedDeque() *lane.Deque {
	return te.failedDeque
}

// Stop 停止执行: 不再派发新任务, 已在执行的任务继续运行直至完成
func (te *TaskExecutor) Stop() {
	atomic.StoreInt32(&te.stopped, 1)
	te.stopClose.Do(func() { close(te.stopSignal()) })
}

func (te *TaskExecutor) stopSignal() chan struct{} {
	te.stopInit.Do(func() { te.stopChan = make(chan struct{}) })
	return te.stopChan
}

// Stopped 返回是否已请求停止
func (te *TaskExecutor) Stopped() bool {
	return atomic.LoadInt32(&te.stopped) == 1
}

// Pause 暂停执行
func (te *TaskExecutor) Pause() {

}

// Resume 恢复执行
func (te *TaskExecutor) Resume() {
}
