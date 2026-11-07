package uploader

import (
	"context"
	"errors"
	"github.com/oleiade/lane"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/waitgroup"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"os"
	"sync"
)

type (
	worker struct {
		id         int
		partOffset int64
		splitUnit  SplitUnit
		checksum   string
	}

	workerList []*worker
)

// CheckSumList 返回所以worker的checksum
// TODO: 实现sort
func (werl *workerList) CheckSumList() []string {
	checksumList := make([]string, 0, len(*werl))
	for _, wer := range *werl {
		checksumList = append(checksumList, wer.checksum)
	}
	return checksumList
}

func (werl *workerList) Readed() int64 {
	var readed int64
	for _, wer := range *werl {
		readed += wer.splitUnit.Readed()
	}
	return readed
}

func (muer *MultiUploader) upload() (uperr error) {
	if muer.isAborted() || muer.isStopped() {
		return muer.abortError()
	}
	ctx, cancel := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-muer.canceled:
		case <-muer.progressFailed:
		case <-ctx.Done():
			return
		}
		cancel()
		if muer.rateLimit != nil {
			muer.rateLimit.Stop()
		}
	}()
	defer func() { cancel(); <-watchDone }()
	originPCSHost, err := muer.multiUpload.Precreate(ctx)
	if muer.isAborted() || ctx.Err() != nil {
		return muer.abortError()
	}
	if err != nil {
		return err
	}
	if err := muer.validateSource(); err != nil {
		return err
	}
	var (
		uploadDeque = lane.NewDeque()
	)

	var (
		checksumMap = make(map[int]string) // key: wer.id, value: checksum
		mu          sync.Mutex
	)
	terminate := func(err error) {
		if err == nil {
			err = errors.New("upload terminated")
		}
		mu.Lock()
		if uperr == nil {
			uperr = err
		}
		mu.Unlock()
		muer.Cancel()
	}

	// 加入队列
	for _, wer := range muer.workers {
		if wer.checksum == "" {
			uploadDeque.Append(wer)
		} else {
			checksumMap[wer.id] = wer.checksum
		}
	}

	for {
		wg := waitgroup.NewWaitGroup(muer.config.Parallel)
		for {
			if muer.isStopped() || muer.isAborted() { // 不再派发新分片
				break
			}

			e := uploadDeque.Shift()
			if e == nil { // 任务为空
				break
			}

			wer := e.(*worker)
			wg.AddDelta()
			if muer.isStopped() || muer.isAborted() {
				wg.Done()
				break
			}
			go func() {
				defer wg.Done()
				if muer.isAborted() {
					return
				}
				if err := muer.validateSource(); err != nil {
					terminate(err)
					return
				}
				checksum, terr := muer.multiUpload.TmpFile(ctx, muer.instanceState.Uploadid, muer.targetPath, wer.id, wer.partOffset, wer.splitUnit)
				if muer.isAborted() {
					return
				}
				if err := muer.validateSource(); err != nil {
					terminate(err)
					return
				}
				if terr != nil {
					if errors.Is(terr, rio.ErrSourceChanged) || errors.Is(terr, ErrSourceRead) {
						terminate(terr)
						return
					}
					var me *MultiError
					if errors.As(terr, &me) {
						if me.Terminated { // 终止
							terminate(me.Err)
							return
						}
					}
					if errors.Is(terr, context.Canceled) {
						muer.Cancel()
						return
					}

					uploaderVerbose.Warnf("upload err: %s, id: %d\n", terr, wer.id)
					if muer.isStopped() { // 优雅停止: 失败的分片不再重试
						return
					}
					wer.splitUnit.Seek(0, os.SEEK_SET)
					uploadDeque.Append(wer)
					return
				}
				muer.stateMu.Lock()
				wer.checksum = checksum
				muer.stateMu.Unlock()
				mu.Lock()
				checksumMap[wer.id] = checksum // 记录成功任务的 checksum
				mu.Unlock()

				// 通知更新
				select {
				case muer.updateInstanceStateChan <- struct{}{}:
				default:
				}
			}()
		}
		wg.Wait()

		if muer.isStopped() || muer.isAborted() {
			break
		}

		// 没有任务了
		if uploadDeque.Size() == 0 {
			break
		}
	}

	if muer.isAborted() {
		if uperr != nil {
			return uperr
		}
		return muer.abortError()
	}

	if muer.isStopped() { // 优雅停止: 文件未完整上传, 不合并 superfile, 交由断点续传处理
		return context.Canceled
	}

	if err := muer.validateSource(); err != nil {
		return err
	}
	if muer.isAborted() || muer.isStopped() || ctx.Err() != nil {
		return muer.abortError()
	}
	cerr := muer.multiUpload.CreateSuperFile(ctx, originPCSHost, muer.config.Policy, muer.instanceState.Uploadid, muer.fileSize, checksumMap)
	if muer.isAborted() || ctx.Err() != nil {
		return muer.abortError()
	}
	if cerr != nil {
		return cerr
	}

	return muer.validateSource()
}
