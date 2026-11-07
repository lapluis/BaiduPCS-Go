// Package downloader 多线程下载器, 重构版
package downloader

import (
	"context"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/waitgroup"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsverbose"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAcceptRanges 默认的 Accept-Ranges
	DefaultAcceptRanges = "bytes"
)

var BlockSizeList = [4]int64{256 * converter.KB, 512 * converter.KB, 1 * converter.MB, 4 * converter.MB}

// ErrResumeSizeMismatch means saved progress belongs to a different file size.
var ErrResumeSizeMismatch = errors.New("download resume size does not match remote file")
var ErrRemoteChanged = errors.New("remote download entity changed")

type (
	// Downloader 下载
	Downloader struct {
		onExecuteEvent        requester.Event    //开始下载事件
		onSuccessEvent        requester.Event    //成功下载事件
		onFinishEvent         requester.Event    //结束下载事件
		onPauseEvent          requester.Event    //暂停下载事件
		onResumeEvent         requester.Event    //恢复下载事件
		onCancelEvent         requester.Event    //取消下载事件
		onDownloadStatusEvent DownloadStatusFunc //状态处理事件

		executeCancelFunc context.CancelFunc
		cancelMu          sync.Mutex
		controlEvents     *downloadControlEvents

		firstInfo               *DownloadFirstInfo      // 初始信息
		loadBalancerCompareFunc LoadBalancerCompareFunc // 负载均衡检测函数
		durlCheckFunc           DURLCheckFunc           // 下载url检测函数
		durlCheckContextFunc    DURLCheckContextFunc
		statusCodeBodyCheckFunc StatusCodeBodyCheckFunc
		executeTime             time.Time
		durl                    string
		loadBalansers           []string
		writer                  io.WriterAt
		client                  *requester.HTTPClient
		config                  *Config
		monitor                 *Monitor
		instanceState           *InstanceState
	}

	// DURLCheckFunc 下载URL检测函数
	DURLCheckFunc func(client *requester.HTTPClient, durl string) (contentLength int64, resp *http.Response, err error)
	// DURLCheckContextFunc receives the execution context, including Cancel().
	DURLCheckContextFunc func(context.Context, *requester.HTTPClient, string) (int64, *http.Response, error)
	// StatusCodeBodyCheckFunc 响应状态码出错的检查函数
	StatusCodeBodyCheckFunc func(respBody io.Reader) error
)

// NewDownloader 初始化Downloader
func NewDownloader(durl string, writer io.WriterAt, config *Config) (der *Downloader) {
	der = &Downloader{
		durl:   durl,
		config: config,
		writer: writer,
	}

	return
}

// SetFirstInfo 设置初始信息
// 如果设置了此值, 将忽略检测url
func (der *Downloader) SetFirstInfo(i *DownloadFirstInfo) {
	der.firstInfo = i
}

// SetClient 设置http客户端
func (der *Downloader) SetClient(client *requester.HTTPClient) {
	der.client = client
}

// SetDURLCheckFunc 设置下载URL检测函数
// New probes should use SetDURLCheckFuncWithContext to support Cancel during I/O.
func (der *Downloader) SetDURLCheckFunc(f DURLCheckFunc) {
	der.durlCheckFunc = f
	der.durlCheckContextFunc = nil
}

// SetDURLCheckFuncWithContext installs a probe that honors execution cancellation.
func (der *Downloader) SetDURLCheckFuncWithContext(f DURLCheckContextFunc) {
	der.durlCheckContextFunc = f
	der.durlCheckFunc = nil
}

func (der *Downloader) SetFileContentLength(length int64) {
	if der.firstInfo == nil {
		der.firstInfo = &DownloadFirstInfo{
			ContentLength: length,
			AcceptRanges:  DefaultAcceptRanges,
		}
	}
}

// SetLoadBalancerCompareFunc 设置负载均衡检测函数
func (der *Downloader) SetLoadBalancerCompareFunc(f LoadBalancerCompareFunc) {
	der.loadBalancerCompareFunc = f
}

// SetStatusCodeBodyCheckFunc 设置响应状态码出错的检查函数, 当FirstCheckMethod不为HEAD时才有效
func (der *Downloader) SetStatusCodeBodyCheckFunc(f StatusCodeBodyCheckFunc) {
	der.statusCodeBodyCheckFunc = f
}

func (der *Downloader) lazyInit() {
	if der.config == nil {
		der.config = NewConfig()
	} else {
		der.config = der.config.Copy()
	}
	der.config.Fix()
	if der.client == nil {
		der.client = requester.NewHTTPClient()
		der.client.SetTimeout(2 * time.Minute)
	}
	if der.monitor == nil {
		der.monitor = NewMonitor()
	}
	if der.loadBalancerCompareFunc == nil {
		der.loadBalancerCompareFunc = DefaultLoadBalancerCompareFunc
	}
}

// SelectParallel 获取合适的 parallel
func (der *Downloader) SelectParallel(single bool, maxParallel int, totalSize int64, instanceRangeList transfer.RangeList) (parallel int) {
	isRange := instanceRangeList != nil && len(instanceRangeList) > 0
	if single { //不支持多线程
		parallel = 1
	} else if isRange {
		parallel = min(len(instanceRangeList), maxParallel)
	} else {
		parallel = maxParallel
		if int64(parallel) > totalSize/MinParallelSize {
			parallel = int(totalSize/MinParallelSize) + 1
		}
	}

	if parallel < 1 {
		parallel = 1
	}
	return
}

// SelectBlockSizeAndInitRangeGen 获取合适的 BlockSize, 和初始化 RangeGen
func (der *Downloader) SelectBlockSizeAndInitRangeGen(single bool, status *transfer.DownloadStatus, parallel int) (blockSize int64, initErr error) {
	// Range 生成器
	if single { // 单线程
		blockSize = -1
		return
	}
	gen := status.RangeListGen()
	if gen == nil {
		switch der.config.Mode {
		case transfer.RangeGenMode_Default:
			gen = transfer.NewRangeListGenDefault(status.TotalSize(), 0, 0, parallel)
			blockSize = gen.LoadBlockSize()
		case transfer.RangeGenMode_BlockSize:
			totalSize := status.TotalSize()
			if totalSize < 5*converter.MB {
				blockSize = BlockSizeList[0]
			} else if totalSize < 10*converter.MB {
				blockSize = BlockSizeList[1]
			} else if totalSize < 15*converter.MB {
				blockSize = BlockSizeList[2]
			} else {
				blockSize = BlockSizeList[3]
			}
			gen = transfer.NewRangeListGenBlockSize(totalSize, 0, blockSize)
		default:
			initErr = transfer.ErrUnknownRangeGenMode
			return
		}
	} else {
		blockSize = gen.LoadBlockSize()
	}
	status.SetRangeListGen(gen)
	return
}

// SelectCacheSize 获取合适的 cacheSize
func (der *Downloader) SelectCacheSize(confCacheSize int, blockSize int64) (cacheSize int) {
	if blockSize > 0 && int64(confCacheSize) > blockSize {
		// 如果 cache size 过高, 则调低
		cacheSize = int(blockSize)
	} else {
		cacheSize = confCacheSize
	}
	return
}

// DefaultDURLCheckFunc 默认的 DURLCheckFunc
func DefaultDURLCheckFunc(client *requester.HTTPClient, durl string) (contentLength int64, resp *http.Response, err error) {
	return defaultDURLCheckWithContext(context.Background(), client, durl)
}

func defaultDURLCheckWithContext(ctx context.Context, client *requester.HTTPClient, durl string) (contentLength int64, resp *http.Response, err error) {
	resp, err = client.ReqWithContext(ctx, http.MethodGet, durl, nil, nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, nil, err
	}
	return resp.ContentLength, resp, nil
}

func (der *Downloader) checkURL(ctx context.Context, client *requester.HTTPClient, url string) (int64, *http.Response, error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if der.durlCheckContextFunc != nil {
		return der.durlCheckContextFunc(ctx, client, url)
	}
	if der.durlCheckFunc != nil {
		return der.durlCheckFunc(client, url)
	}
	return defaultDURLCheckWithContext(ctx, client, url)
}

func (der *Downloader) checkLoadBalancers(ctx context.Context) *LoadBalancerResponseList {
	var (
		responsesMu           sync.Mutex
		loadBalancerResponses = make([]*LoadBalancerResponse, 0, len(der.loadBalansers)+1)
		handleLoadBalancer    = func(req *http.Request) {
			if req == nil {
				return
			}

			if der.config.TryHTTP {
				req.URL.Scheme = "http"
			}

			loadBalancer := &LoadBalancerResponse{
				URL:     req.URL.String(),
				Referer: req.Referer(),
			}

			responsesMu.Lock()
			loadBalancerResponses = append(loadBalancerResponses, loadBalancer)
			responsesMu.Unlock()
			pcsverbose.Verbosef("DEBUG: load balance task: URL: %s, Referer: %s\n", loadBalancer.URL, loadBalancer.Referer)
		}
	)

	// 加入第一个
	loadBalancerResponses = append(loadBalancerResponses, &LoadBalancerResponse{
		URL: der.durl,
	})

	// 多下载服务器的负载均衡, 在本项目中无意义，因为百度只用单下载服务器
	wg := waitgroup.NewWaitGroup(4)
	// Share the transport and cookie jar, but isolate request policy from
	// callers that may be using this client concurrently.
	probeClient := *der.client
	probeClient.SetTimeout(5 * time.Second)
	for _, loadBalanser := range der.loadBalansers { // 这里服务器列表数量为0，所以逻辑实际未生效
		wg.AddDelta()
		go func(loadBalanser string) {
			defer wg.Done()

			subContentLength, subResp, subErr := der.checkURL(ctx, &probeClient, loadBalanser)
			if subResp != nil {
				// Error decoders may need the body before this probe finishes.
				defer subResp.Body.Close()
			}
			if subErr != nil {
				pcsverbose.Verbosef("DEBUG: loadBalanser Error: %s\n", subErr)
				return
			}

			// 检测状态码
			switch subResp.StatusCode / 100 {
			case 2: // succeed
			case 4, 5: // error
				var err error
				if der.statusCodeBodyCheckFunc != nil {
					err = der.statusCodeBodyCheckFunc(subResp.Body)
				} else {
					err = errors.New(subResp.Status)
				}
				pcsverbose.Verbosef("DEBUG: loadBalanser Status Error: %s\n", err)
				return
			}

			// 检测长度
			if der.firstInfo.ContentLength != subContentLength {
				pcsverbose.Verbosef("DEBUG: loadBalanser Content-Length not equal to main server\n")
				return
			}

			if !der.loadBalancerCompareFunc(der.firstInfo.ToMap(), subResp) {
				pcsverbose.Verbosef("DEBUG: loadBalanser not equal to main server\n")
				return
			}

			handleLoadBalancer(subResp.Request)
		}(loadBalanser)
	}
	wg.Wait()

	loadBalancerResponseList := NewLoadBalancerResponseList(loadBalancerResponses)
	return loadBalancerResponseList
}

// Execute 开始任务
func (der *Downloader) Execute() error {
	return der.ExecuteWithContext(context.Background())
}

// ExecuteWithContext cancels active ranges and persists their remaining work.
func (der *Downloader) ExecuteWithContext(ctx context.Context) (resultErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	controls := &downloadControlEvents{accepting: true, cancel: cancel}
	der.cancelMu.Lock()
	der.executeCancelFunc = cancel
	der.controlEvents = controls
	der.cancelMu.Unlock()
	defer cancel()
	defer func() {
		if controlErr := der.joinControlEvents(controls); controlErr != nil && !errors.Is(resultErr, controlErr) {
			resultErr = errors.Join(resultErr, controlErr)
		}
		// A joined observer can cancel after the last ordinary return check.
		// The execution's own deferred cancel has not run yet.
		if canceled := ctx.Err(); canceled != nil && !errors.Is(resultErr, canceled) {
			resultErr = errors.Join(resultErr, canceled)
		}
		der.cancelMu.Lock()
		if der.controlEvents == controls {
			der.controlEvents = nil
			der.executeCancelFunc = nil
		}
		der.cancelMu.Unlock()
	}()
	der.lazyInit()
	var (
		resp *http.Response
	)
	if der.firstInfo == nil {
		// 检测
		var contentLength int64
		var err error
		contentLength, resp, err = der.checkURL(ctx, der.client, der.durl)
		if resp != nil {
			defer resp.Body.Close()
		}
		if err != nil {
			return err
		}

		// 检测网络错误
		switch resp.StatusCode / 100 {
		case 2: // succeed
		case 4, 5: // error
			if der.statusCodeBodyCheckFunc != nil {
				err = der.statusCodeBodyCheckFunc(resp.Body)
				resp.Body.Close() // 关闭连接
				if err != nil {
					return err
				}
			}
			return errors.New(resp.Status)
		}

		acceptRanges := resp.Header.Get("Accept-Ranges")
		if contentLength < 0 || (resp.StatusCode != http.StatusPartialContent && acceptRanges != DefaultAcceptRanges) || (resp.StatusCode == http.StatusOK && resp.Request != nil && resp.Request.Header.Get("Range") != "") {
			acceptRanges = ""
		} else {
			acceptRanges = DefaultAcceptRanges
		}

		// 初始化firstInfo
		der.firstInfo = NewDownloadFirstInfoByResp(contentLength, resp)
		der.firstInfo.AcceptRanges = acceptRanges
		pcsverbose.Verbosef("DEBUG: download task: URL: %s, Referer: %s\n", resp.Request.URL, resp.Request.Referer())
	} else {
		if der.firstInfo.AcceptRanges == "" {
			der.firstInfo.AcceptRanges = DefaultAcceptRanges
		}
	}
	if strings.HasPrefix(der.firstInfo.ETag, "W/") {
		// Weak validators cannot prove byte identity across ranges and MUST
		// NOT be used in If-Range (RFC 9110 section 13.1.5). Fetch one complete
		// representation instead of combining separately requested pieces.
		der.firstInfo.AcceptRanges = ""
	}
	if der.firstInfo.AcceptRanges == "" && resp != nil && resp.StatusCode == http.StatusPartialContent {
		// A partial probe cannot serve as a complete stream, including when
		// its total size is unknown and EOF only ends the prefix response.
		resp.Body.Close()
		resp = nil
	}

	var (
		loadBalancerResponseList = der.checkLoadBalancers(ctx)
		single                   = der.firstInfo.AcceptRanges == ""
		bii                      *transfer.DownloadInstanceInfo
	)
	if err := ctx.Err(); err != nil {
		return err
	}

	// Single streams write the same output as ranged attempts. They must own
	// its checkpoint too, even though their progress cannot be resumed.
	if err := der.initInstanceState(der.config.InstanceStateStorageFormat); err != nil {
		return err
	}
	defer func() {
		der.joinControlEvents(controls)
		der.instanceState.Close()
	}()
	if single {
		if err := der.instanceState.discardRanges(); err != nil {
			return err
		}
	} else {
		//load breakpoint
		var err error
		bii, err = der.instanceState.Load()
		if err != nil {
			der.instanceState.Close()
			return err
		}
		if bii != nil && bii.DownloadStatus != nil && bii.DownloadStatus.TotalSize() != der.firstInfo.ContentLength {
			der.instanceState.Close()
			return fmt.Errorf("%w: saved=%d, remote=%d", ErrResumeSizeMismatch, bii.DownloadStatus.TotalSize(), der.firstInfo.ContentLength)
		}
		if bii != nil && bii.DownloadStatus != nil {
			if err := der.validateResumeFile(bii); err != nil {
				der.instanceState.Close()
				return err
			}
		}
	}

	var (
		isInstance = bii != nil // 是否存在断点信息
		status     *transfer.DownloadStatus
	)
	if !isInstance {
		bii = &transfer.DownloadInstanceInfo{}
	} else if resp != nil {
		// Resumed ranges do not reuse the prefix probe. Close it before
		// opening the configured workers so it consumes no extra connection.
		resp.Body.Close()
	}

	if bii.DownloadStatus != nil {
		// 使用断点信息的状态
		status = bii.DownloadStatus
	} else {
		// 新建状态
		status = transfer.NewDownloadStatus()
		status.SetTotalSize(der.firstInfo.ContentLength)
	}
	if single && der.firstInfo.ContentLength < 0 {
		status.SetTotalSize(0)
	}

	// 设置限速
	if der.config.MaxRate > 0 {
		rl := speeds.NewRateLimit(der.config.MaxRate)
		status.SetRateLimit(rl)
		defer rl.Stop()
	}

	// 数据处理
	parallel := der.SelectParallel(single, der.config.MaxParallel, status.TotalSize(), bii.Ranges) // 实际的下载并行量
	blockSize, err := der.SelectBlockSizeAndInitRangeGen(single, status, parallel)                 // 实际的BlockSize
	if err != nil {
		return err
	}

	cacheSize := der.SelectCacheSize(der.config.CacheSize, blockSize) // 实际下载缓存

	pcsverbose.Verbosef("DEBUG: download task CREATED: parallel: %d, cache size: %d\n", parallel, cacheSize)

	der.monitor.InitMonitorCapacity(parallel)

	var writer Writer
	if !der.config.IsTest {
		// 磁盘预分配逻辑, 现在固态硬盘已成主流因此废弃
		//if fder, ok := der.writer.(Fder); ok {
		//	err = prealloc.PreAlloc(fder.Fd(), status.TotalSize())
		//	if err != nil {
		//		pcsverbose.Verbosef("DEBUG: truncate file error: %s\n", err)
		//	}
		//}
		writer = der.writer // 非测试模式, 赋值writer
	}

	// 数据平均分配给各个线程
	isRange := bii.Ranges != nil && len(bii.Ranges) > 0
	if !isRange {
		// 没有使用断点续传
		// 分配线程
		bii.Ranges = make(transfer.RangeList, 0, parallel)
		if single { // 单线程
			bii.Ranges = append(bii.Ranges, &transfer.Range{})
		} else {
			gen := status.RangeListGen()
			for i := 0; i < cap(bii.Ranges); i++ {
				_, r := gen.GenRange()
				if r == nil { // 没有了（不正常）
					break
				}
				bii.Ranges = append(bii.Ranges, r)
			}
		}
	}

	var (
		writeMu = &sync.Mutex{}
	)
	activeRanges := bii.Ranges
	if len(activeRanges) > parallel {
		// Retain unassigned saved ranges in the monitor's checkpointed queue.
		// Dropping them would mark their missing bytes as already downloaded.
		der.monitor.pendingRanges = append(transfer.RangeList(nil), activeRanges[parallel:]...)
		activeRanges = activeRanges[:parallel]
	}
	for k, r := range activeRanges {
		loadBalancer := loadBalancerResponseList.SequentialGet()
		if loadBalancer == nil {
			continue
		}

		worker := NewWorker(k, loadBalancer.URL, writer)
		worker.cacheSize = cacheSize
		worker.SetClient(der.client)
		worker.SetWriteMutex(writeMu)
		worker.SetReferer(loadBalancer.Referer)
		worker.etag = der.firstInfo.ETag
		worker.lastModified = der.firstInfo.LastModified
		worker.SetTotalSize(der.firstInfo.ContentLength)

		// 使用第一个连接
		// 断点续传时不使用
		if k == 0 && !isInstance {
			worker.firstResp = resp
		}

		worker.SetAcceptRange(der.firstInfo.AcceptRanges)
		worker.SetRange(r) // 分配Range
		der.monitor.Append(worker)
	}

	der.monitor.SetStatus(status)

	// 服务器不支持断点续传, 或者单线程下载, 都不重载worker
	der.monitor.SetReloadWorker(parallel > 1)

	moniterCtx, moniterCancelFunc := context.WithCancel(ctx)
	defer moniterCancelFunc()

	if single {
		// Single streams cannot describe restartable byte ranges, but retain
		// the checkpoint lease until the transfer and cleanup have finished.
		der.monitor.SetInstanceState(nil)
	} else {
		der.monitor.SetInstanceState(der.instanceState)
	}

	// 开始执行
	der.executeTime = time.Now()
	der.monitor.prepareCompletion()
	if eventErr := runDownloadEvent("execute", der.onExecuteEvent); eventErr != nil {
		// Close monitor completion without starting workers, then preserve a
		// recoverable snapshot before notifying the final observer.
		moniterCancelFunc()
		der.monitor.execute(moniterCtx, nil)
		err = errors.Join(eventErr, der.joinControlEvents(controls), der.monitor.saveCheckpoint())
		err = errors.Join(err, runDownloadEvent("finish", der.onFinishEvent))
		return errors.Join(err, ctx.Err())
	}
	progress := der.downloadStatusEvent()
	var progressFailed <-chan struct{}
	if progress != nil {
		progressFailed = progress.failed
	}
	der.monitor.execute(moniterCtx, progressFailed)
	var progressErr error
	if progress != nil {
		<-progress.done
		if progress.err != nil {
			// Workers and the callback have both stopped. This also handles a
			// callback that panics after the network transfer already completed.
			progressErr = errors.Join(progress.err, der.monitor.saveCheckpoint())
		}
	}

	// 检查错误
	controlErr := der.joinControlEvents(controls)
	err = errors.Join(der.monitor.Err(), progressErr, controlErr)
	retainCancellation := func() {
		if canceled := ctx.Err(); canceled != nil && !errors.Is(err, canceled) {
			err = errors.Join(err, canceled)
		}
	}
	// A progress callback may cancel after the monitor has already finished.
	retainCancellation()
	if err == nil && single && der.firstInfo.ContentLength >= 0 && status.Downloaded() != der.firstInfo.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	finalSize := der.firstInfo.ContentLength
	if single && finalSize < 0 {
		// EOF determines the final size of a successful unknown-length
		// stream. Its new contents may be shorter than the existing file.
		finalSize = status.Downloaded()
	}
	if err == nil && !der.config.IsTest && finalSize >= 0 {
		if file, ok := der.writer.(interface{ Truncate(int64) error }); ok {
			err = file.Truncate(finalSize)
		}
	}
	retainCancellation()
	if err == nil && !der.config.IsTest {
		if file, ok := der.writer.(interface{ Sync() error }); ok {
			// Flush data and truncation before announcing success or discarding
			// the ranges that are needed to recover from a delayed disk error.
			err = file.Sync()
		}
	}
	retainCancellation()
	if ctx.Err() != nil && der.monitor.Err() == nil && progressErr == nil || controlErr != nil {
		// The monitor finished before cancellation and did not persist a final
		// cancellation snapshot. Save only after all workers/callbacks joined.
		retainCancellation()
		err = errors.Join(err, der.monitor.saveCheckpoint())
	}
	if err == nil {
		err = der.removeInstanceState()
	}
	if err == nil { // 成功
		err = runDownloadEvent("success", der.onSuccessEvent)
	}

	// 执行结束
	err = errors.Join(err, runDownloadEvent("finish", der.onFinishEvent))
	retainCancellation()
	return err
}

type downloadProgressEvent struct {
	done, failed chan struct{}
	err          error // Read only after done closes.
}

// downloadStatusEvent 执行状态处理事件
func (der *Downloader) downloadStatusEvent() *downloadProgressEvent {
	callback := der.onDownloadStatusEvent
	if callback == nil {
		return nil
	}

	monitor := der.monitor
	status := monitor.Status()
	completed := monitor.CompletedChan()
	progress := &downloadProgressEvent{done: make(chan struct{}), failed: make(chan struct{})}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				cause, ok := r.(error)
				if !ok {
					cause = fmt.Errorf("%v", r)
				}
				progress.err = fmt.Errorf("%w: download progress callback panic: %w", ErrProgressCallback, cause)
				close(progress.failed)
			}
			close(progress.done)
		}()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-completed:
				return
			case <-ticker.C:
				// Do not start another callback if completion and a tick are ready.
				select {
				case <-completed:
					return
				default:
				}
				callback(status, monitor.RangeWorker)
			}
		}
	}()
	return progress
}

// Pause 暂停
func (der *Downloader) Pause() {
	if der.monitor == nil {
		return
	}
	notify := der.prepareControlEvent("pause", der.onPauseEvent)
	der.monitor.Pause()
	if notify != nil {
		go notify()
	}
}

// Resume 恢复
func (der *Downloader) Resume() {
	if der.monitor == nil {
		return
	}
	notify := der.prepareControlEvent("resume", der.onResumeEvent)
	der.monitor.Resume()
	if notify != nil {
		go notify()
	}
}

// Cancel 取消
// It signals the current execution immediately, including context-aware probes.
func (der *Downloader) Cancel() {
	der.cancelMu.Lock()
	cancel := der.executeCancelFunc
	notify := der.prepareControlEventLocked("cancel", der.onCancelEvent)
	der.cancelMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if notify != nil {
		go notify()
	}
}

// OnExecute 设置开始下载事件
// The callback runs synchronously after setup and before any worker starts.
// It may configure or cancel work, but must not wait for worker completion.
// A panic is returned as ErrEventCallback after checkpoint and finish handling.
func (der *Downloader) OnExecute(onExecuteEvent requester.Event) {
	der.onExecuteEvent = onExecuteEvent
}

// OnSuccess 设置成功下载事件
// The callback runs synchronously after data flush and checkpoint cleanup.
// A panic is returned as ErrEventCallback; completed data remains committed.
func (der *Downloader) OnSuccess(onSuccessEvent requester.Event) {
	der.onSuccessEvent = onSuccessEvent
}

// OnFinish 设置结束下载事件
// The callback runs synchronously before a started download returns.
// A panic is joined with any existing transfer error as ErrEventCallback.
func (der *Downloader) OnFinish(onFinishEvent requester.Event) {
	der.onFinishEvent = onFinishEvent
}

// OnPause 设置暂停下载事件
// Control events run asynchronously after signaling the action. Execute joins
// accepted events before final callbacks; they must not wait for Execute to return.
func (der *Downloader) OnPause(onPauseEvent requester.Event) {
	der.onPauseEvent = onPauseEvent
}

// OnResume 设置恢复下载事件
// It follows the same asynchronous completion contract as OnPause.
func (der *Downloader) OnResume(onResumeEvent requester.Event) {
	der.onResumeEvent = onResumeEvent
}

// OnCancel 设置取消下载事件
// It follows the same asynchronous completion contract as OnPause.
func (der *Downloader) OnCancel(onCancelEvent requester.Event) {
	der.onCancelEvent = onCancelEvent
}

// OnDownloadStatusEvent 设置状态处理函数
// Execute waits for an in-flight callback before final events and returning.
// A callback may cancel the download, but must not wait for Execute to return.
func (der *Downloader) OnDownloadStatusEvent(f DownloadStatusFunc) {
	der.onDownloadStatusEvent = f
}
