package uploader

import (
	"context"
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"sync"
	"time"
)

type (
	// MultiUpload 支持多线程的上传, 可用于断点续传
	MultiUpload interface {
		Precreate() (pcsHost string, err pcserror.Error)
		TmpFile(ctx context.Context, uploadid, targetPath string, partseq int, partOffset int64, readerlen64 rio.ReaderLen64) (checksum string, terr error)
		CreateSuperFile(pcsHost, policy, uploadId string, fileSize int64, checksumMap map[int]string) (cerr error)
	}

	// MultiUploader 多线程上传
	MultiUploader struct {
		onExecuteEvent      requester.Event        //开始上传事件
		onSuccessEvent      requester.Event        //成功上传事件
		onFinishEvent       requester.Event        //结束上传事件
		onCancelEvent       requester.Event        //取消上传事件
		onErrorEvent        requester.EventOnError //上传出错事件
		onUploadStatusEvent UploadStatusFunc       //上传状态事件

		instanceState *InstanceState
		stateMu       sync.RWMutex
		finishOnce    sync.Once
		statusWG      sync.WaitGroup

		multiUpload MultiUpload       // 上传体接口
		file        rio.ReaderAtLen64 // 上传
		fileSize    int64             // 执行时固定的文件大小
		config      *MultiUploaderConfig
		workers     workerList
		speedsStat  *speeds.Speeds
		rateLimit   *speeds.RateLimit
		targetPath  string

		executeTime             time.Time
		finished                chan struct{}
		canceled                chan struct{}
		closeCanceledOnce       sync.Once
		stopped                 chan struct{} // 优雅停止信号: 不再派发新分片, 进行中的分片继续完成
		stopOnce                sync.Once
		updateInstanceStateChan chan struct{}
	}

	// MultiUploaderConfig 多线程上传配置
	MultiUploaderConfig struct {
		Parallel       int          // 上传并发量
		BlockSize      int64        // 上传分块
		MaxRate        int64        // 限制最大上传速度
		Policy         string       // 文件重名策略
		ValidateSource func() error // 可选：校验更早阶段捕获的文件状态
	}
)

// NewMultiUploader 初始化上传
func NewMultiUploader(multiUpload MultiUpload, file rio.ReaderAtLen64, config *MultiUploaderConfig, targetPath string) *MultiUploader {
	return &MultiUploader{
		multiUpload: multiUpload,
		file:        file,
		config:      config,
		targetPath:  targetPath,

		finished:                make(chan struct{}, 1),
		canceled:                make(chan struct{}),
		stopped:                 make(chan struct{}),
		updateInstanceStateChan: make(chan struct{}, 1),
	}
}

// SetInstanceState 设置InstanceState, 断点续传信息
func (muer *MultiUploader) SetInstanceState(is *InstanceState) {
	muer.stateMu.Lock()
	defer muer.stateMu.Unlock()
	muer.instanceState = is
}

func (muer *MultiUploader) lazyInit() {
	muer.stateMu.Lock()
	if muer.instanceState == nil {
		muer.instanceState = &InstanceState{}
	}
	muer.stateMu.Unlock()
	if muer.finished == nil {
		muer.finished = make(chan struct{}, 1)
	}
	if muer.canceled == nil {
		muer.canceled = make(chan struct{})
	}
	if muer.stopped == nil {
		muer.stopped = make(chan struct{})
	}
	if muer.updateInstanceStateChan == nil {
		muer.updateInstanceStateChan = make(chan struct{}, 1)
	}
	if muer.config == nil {
		muer.config = &MultiUploaderConfig{}
	}
	if muer.config.Parallel <= 0 {
		muer.config.Parallel = 4
	}
	if muer.config.BlockSize <= 0 {
		muer.config.BlockSize = 16 * converter.MB
	}
	if muer.speedsStat == nil {
		muer.speedsStat = &speeds.Speeds{}
	}
}

func (muer *MultiUploader) check() {
	if muer.file == nil {
		panic("file is nil")
	}
	if muer.multiUpload == nil {
		panic("multiUpload is nil")
	}
}

// Execute 执行上传
func (muer *MultiUploader) Execute() {
	muer.check()
	muer.lazyInit()
	// 初始化限速
	if muer.config.MaxRate > 0 {
		muer.rateLimit = speeds.NewRateLimit(muer.config.MaxRate)
		defer muer.rateLimit.Stop()
	}

	// 分配任务
	muer.fileSize = muer.file.Len()
	if err := muer.validateSource(); err != nil {
		muer.finish(err)
		return
	}
	if err := validateUploadState(muer.instanceState, muer.fileSize); err != nil {
		muer.finish(err)
		return
	}
	var workers workerList
	if muer.instanceState != nil && len(muer.instanceState.BlockList) > 0 {
		workers = muer.getWorkerListByInstanceState(muer.instanceState)
		uploaderVerbose.Infof("upload task CREATED from instance state\n")
	} else {
		workers = muer.getWorkerListByInstanceState(&InstanceState{
			BlockList: SplitBlock(muer.fileSize, muer.config.BlockSize),
		})

		uploaderVerbose.Infof("upload task CREATED: block size: %d, num: %d\n", muer.config.BlockSize, len(workers))
	}
	muer.stateMu.Lock()
	muer.workers = workers
	muer.stateMu.Unlock()

	// 开始上传
	muer.executeTime = time.Now()
	pcsutil.Trigger(muer.onExecuteEvent)

	muer.uploadStatusEvent()

	err := muer.upload()
	muer.finish(err)
}

func (muer *MultiUploader) validateSource() error {
	if muer.config.ValidateSource != nil {
		if err := muer.config.ValidateSource(); err != nil {
			return errors.Join(rio.ErrSourceChanged, err)
		}
	}
	if validator, ok := muer.file.(rio.SnapshotValidator); ok {
		if err := validator.ValidateSnapshot(); err != nil {
			return err
		}
	}
	if muer.file.Len() != muer.fileSize {
		return rio.ErrSourceChanged
	}
	return nil
}

func (muer *MultiUploader) finish(err error) {
	// 完成
	muer.finishOnce.Do(func() { close(muer.finished) })
	muer.statusWG.Wait()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if muer.onCancelEvent != nil {
				muer.onCancelEvent()
			}
		} else if muer.onErrorEvent != nil {
			muer.onErrorEvent(err)
		}
	} else {
		pcsutil.TriggerOnSync(muer.onSuccessEvent)
	}
	pcsutil.TriggerOnSync(muer.onFinishEvent)
}

// InstanceState 返回断点续传信息
func (muer *MultiUploader) InstanceState() *InstanceState {
	muer.stateMu.RLock()
	defer muer.stateMu.RUnlock()
	blockStates := make([]*BlockState, 0, len(muer.workers))
	for _, wer := range muer.workers {
		blockStates = append(blockStates, &BlockState{
			ID:       wer.id,
			Range:    wer.splitUnit.Range(),
			CheckSum: wer.checksum,
		})
	}
	uploadID := ""
	if muer.instanceState != nil {
		uploadID = muer.instanceState.Uploadid
	}
	return &InstanceState{
		BlockList: blockStates,
		Uploadid:  uploadID,
	}
}

func (muer *MultiUploader) isCanceled() bool {
	select {
	case <-muer.canceled:
		return true
	default:
		return false
	}
}

// Cancel 取消上传
func (muer *MultiUploader) Cancel() {
	muer.closeCanceledOnce.Do(func() {
		close(muer.canceled)
	})
}

// GracefulStop 优雅停止: 不再派发新分片, 进行中的分片继续执行直至完成
func (muer *MultiUploader) GracefulStop() {
	muer.stopOnce.Do(func() {
		close(muer.stopped)
	})
}

// isStopped 返回是否已请求优雅停止
func (muer *MultiUploader) isStopped() bool {
	select {
	case <-muer.stopped:
		return true
	default:
		return false
	}
}

// OnExecute 设置开始上传事件
func (muer *MultiUploader) OnExecute(onExecuteEvent requester.Event) {
	muer.onExecuteEvent = onExecuteEvent
}

// OnSuccess 设置成功上传事件
func (muer *MultiUploader) OnSuccess(onSuccessEvent requester.Event) {
	muer.onSuccessEvent = onSuccessEvent
}

// OnFinish 设置结束上传事件
func (muer *MultiUploader) OnFinish(onFinishEvent requester.Event) {
	muer.onFinishEvent = onFinishEvent
}

// OnCancel 设置取消上传事件
func (muer *MultiUploader) OnCancel(onCancelEvent requester.Event) {
	muer.onCancelEvent = onCancelEvent
}

// OnError 设置上传发生错误事件
func (muer *MultiUploader) OnError(onErrorEvent requester.EventOnError) {
	muer.onErrorEvent = onErrorEvent
}

// OnUploadStatusEvent 设置上传状态事件
func (muer *MultiUploader) OnUploadStatusEvent(f UploadStatusFunc) {
	muer.onUploadStatusEvent = f
}
