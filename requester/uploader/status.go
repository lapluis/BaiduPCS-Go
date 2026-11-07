package uploader

import (
	"fmt"
	"math"
	"time"
)

type (
	// Status 上传状态接口
	Status interface {
		TotalSize() int64           // 总大小
		Uploaded() int64            // 已上传数据
		SpeedsPerSecond() int64     // 每秒的上传速度
		TimeElapsed() time.Duration // 上传时间
	}

	// UploadStatus 上传状态
	UploadStatus struct {
		totalSize       int64         // 总大小
		uploaded        int64         // 已上传数据
		speedsPerSecond int64         // 每秒的上传速度
		timeElapsed     time.Duration // 上传时间
	}

	UploadStatusFunc func(status Status, updateChan <-chan struct{})
)

// TotalSize 返回总大小
func (us *UploadStatus) TotalSize() int64 {
	return us.totalSize
}

// Uploaded 返回已上传数据
func (us *UploadStatus) Uploaded() int64 {
	return us.uploaded
}

// SpeedsPerSecond 返回每秒的上传速度
func (us *UploadStatus) SpeedsPerSecond() int64 {
	return us.speedsPerSecond
}

// TimeElapsed 返回上传时间
func (us *UploadStatus) TimeElapsed() time.Duration {
	return us.timeElapsed
}

// GetStatusChan 获取上传状态；上传结束时关闭，即使调用方已停止读取。
func (u *Uploader) GetStatusChan() <-chan Status {
	u.initSignals()
	c := make(chan Status)
	go func() {
		defer close(c)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		u.streamStatus(c, ticker.C)
	}()
	return c
}

func (u *Uploader) streamStatus(c chan<- Status, ticks <-chan time.Time) {
	u.streamStatusAt(c, ticks, time.Now)
}

func (u *Uploader) streamStatusAt(c chan<- Status, ticks <-chan time.Time, now func() time.Time) {
	select {
	case <-u.finished:
		return
	case <-u.started:
	}
	// Closing started publishes executeTime and totalSize before they are read.
	// Include bytes read before the status goroutine starts. Sample times belong
	// to the byte snapshots, even when a consumer delays receiving an update.
	var old int64
	sampledAt := u.executeTime
	for {
		select {
		case <-u.finished:
			return
		case <-ticks:
			readed := u.readed64.Readed()
			sampledNow := now()
			var rate int64
			if elapsed := sampledNow.Sub(sampledAt); elapsed > 0 {
				perSecond := float64(readed-old) / elapsed.Seconds()
				if perSecond >= float64(math.MaxInt64) {
					rate = math.MaxInt64
				} else if perSecond > 0 {
					rate = int64(perSecond)
				}
				old, sampledAt = readed, sampledNow
			}
			status := &UploadStatus{
				totalSize: u.totalSize, uploaded: readed,
				speedsPerSecond: rate,
				timeElapsed:     max(0, sampledNow.Sub(u.executeTime)) / (10 * time.Millisecond) * (10 * time.Millisecond),
			}
			select {
			case <-u.finished:
				return
			case c <- status:
			}
		}
	}
}

// 使用中的速度状态
func (muer *MultiUploader) uploadStatusEvent() {
	if muer.onUploadStatusEvent == nil {
		return
	}

	muer.statusWG.Add(1)
	go func() {
		defer muer.statusWG.Done()
		defer func() {
			if r := recover(); r != nil {
				cause, ok := r.(error)
				if !ok {
					cause = fmt.Errorf("%v", r)
				}
				muer.progressErr = fmt.Errorf("%w: %w", ErrProgressCallback, cause)
				close(muer.progressFailed)
			}
		}()
		ticker := time.NewTicker(3 * time.Second) // 每3秒统计
		//ticker := time.NewTicker(990 * time.Millisecond) // 每秒统计
		defer ticker.Stop()
		for {
			select {
			case <-muer.finished:
				return
			case <-ticker.C:
				readed := muer.workers.Readed()
				muer.onUploadStatusEvent(&UploadStatus{
					totalSize:       muer.fileSize,
					uploaded:        readed,
					speedsPerSecond: muer.speedsStat.GetSpeeds(),
					timeElapsed:     time.Since(muer.executeTime) / 1e8 * 1e8,
				}, muer.updateInstanceStateChan)
			}
		}
	}()
}
