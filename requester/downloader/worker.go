package downloader

import (
	"context"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/cachepool"
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

type (
	//Worker 工作单元
	Worker struct {
		totalSize          int64 // 整个文件的大小, worker请求range时会获取尝试获取该值, 如果不匹配, 则返回错误
		cacheSize          int
		wrange             *transfer.Range
		speedsStat         speeds.Speeds // Zero value is ready for concurrent observation.
		id                 int           //id
		url                string        //下载地址
		referer            string        //来源地址
		etag, lastModified string
		acceptRanges       string
		client             *requester.HTTPClient
		firstResp          *http.Response // 第一个响应
		writerAt           io.WriterAt
		writeMu            *sync.Mutex
		execMu             sync.Mutex
		runWG              sync.WaitGroup
		parentCtx          context.Context
		controlMu          sync.Mutex
		paused             bool
		pauseVersion       uint64
		cancelVersion      uint64
		pauseCancel        context.CancelFunc
		retryAfter         time.Time // Protected by controlMu.

		workerCancelFunc context.CancelFunc
		resetFunc        context.CancelFunc
		errMu            sync.RWMutex
		lastError        error // Protected by errMu; progress reads during Execute.
		status           WorkerStatus
		downloadStatus   *transfer.DownloadStatus //总的下载状态
	}

	// WorkerList worker列表
	WorkerList []*Worker
)

// Duplicate 构造新的列表
func (wl WorkerList) Duplicate() WorkerList {
	n := make(WorkerList, len(wl))
	copy(n, wl)
	return n
}

// NewWorker 初始化Worker
func NewWorker(id int, durl string, writerAt io.WriterAt) *Worker {
	return &Worker{
		totalSize: -1,
		id:        id,
		url:       durl,
		writerAt:  writerAt,
		writeMu:   &sync.Mutex{},
	}
}

// ID 返回worker ID
func (wer *Worker) ID() int {
	return wer.id
}

func (wer *Worker) lazyInit() {
	if wer.client == nil {
		wer.client = requester.NewHTTPClient()
	}
	if wer.wrange == nil {
		wer.wrange = &transfer.Range{}
	}
	if wer.wrange.LoadBegin() == 0 && wer.wrange.LoadEnd() == 0 {
		// 取消多线程下载
		wer.acceptRanges = ""
		wer.wrange.StoreEnd(-2)
	}
}

// SetTotalSize 设置整个文件的大小, worker请求range时会获取尝试获取该值, 如果不匹配, 则返回错误
func (wer *Worker) SetTotalSize(size int64) {
	wer.totalSize = size
}

// SetClient 设置http客户端
func (wer *Worker) SetClient(c *requester.HTTPClient) {
	wer.client = c
}

// SetAcceptRange 设置AcceptRange
func (wer *Worker) SetAcceptRange(acceptRanges string) {
	wer.acceptRanges = acceptRanges
}

// SetRange 设置请求范围
func (wer *Worker) SetRange(r *transfer.Range) {
	if wer.wrange == nil {
		wer.wrange = r
		return
	}
	wer.wrange.StoreBegin(r.LoadBegin())
	wer.wrange.StoreEnd(r.LoadEnd())
}

// SetReferer 设置来源
func (wer *Worker) SetReferer(referer string) {
	wer.referer = referer
}

// SetWriteMutex 设置数据写锁
func (wer *Worker) SetWriteMutex(mu *sync.Mutex) {
	wer.writeMu = mu
}

// SetDownloadStatus 增加其他需要统计的数据
func (wer *Worker) SetDownloadStatus(downloadStatus *transfer.DownloadStatus) {
	wer.downloadStatus = downloadStatus
}

// GetStatus 返回下载状态
func (wer *Worker) GetStatus() WorkerStatuser {
	// 空接口与空指针不等价
	return &wer.status
}

// GetRange 返回worker范围
func (wer *Worker) GetRange() *transfer.Range {
	return wer.wrange
}

// GetSpeedsPerSecond 获取每秒的速度
func (wer *Worker) GetSpeedsPerSecond() int64 {
	return wer.speedsStat.GetSpeeds()
}

// Pause 暂停下载
func (wer *Worker) Pause() {
	wer.controlMu.Lock()
	defer wer.controlMu.Unlock()
	if wer.acceptRanges == "" {
		pcsverbose.Verbosef("WARNING: worker unsupport pause")
		return
	}

	if wer.paused || wer.Completed() || wer.status.StatusCode() == StatusCodeInternalError {
		return
	}
	wer.paused = true
	wer.pauseVersion++
	wer.status.SetStatusCode(StatusCodePaused)
	if wer.pauseCancel != nil {
		wer.pauseCancel()
	}
}

// Resume 恢复下载
func (wer *Worker) Resume() {
	wer.resume(true)
}

// resume can clear a startup pause without launching a worker before its
// monitor has installed the task context and registered the initial workers.
func (wer *Worker) resume(start bool) {
	wer.controlMu.Lock()
	if !wer.paused {
		wer.controlMu.Unlock()
		return
	}
	wer.paused = false
	wer.status.SetStatusCode(StatusCodeInit)
	wer.controlMu.Unlock()
	if start {
		wer.runAsync()
	}
}

// Cancel 取消下载
func (wer *Worker) Cancel() error {
	wer.controlMu.Lock()
	cancel := wer.workerCancelFunc
	if cancel == nil {
		wer.controlMu.Unlock()
		return errors.New("cancelFunc not set")
	}
	wer.cancelVersion++
	wer.controlMu.Unlock()
	cancel()
	return nil
}

// Reset 重设连接
func (wer *Worker) Reset() {
	wer.tryReset()
}

// tryReset reports whether a restart was accepted, so monitors only charge
// their connection budget when a new execution has actually been scheduled.
func (wer *Worker) tryReset() bool {
	wer.controlMu.Lock()
	defer wer.controlMu.Unlock()
	if wer.paused || wer.Completed() || time.Now().Before(wer.retryAfter) {
		return false
	}
	if wer.resetFunc == nil {
		pcsverbose.Verbosef("DEBUG: worker: resetFunc not set")
		return false
	}
	wer.resetFunc()
	wer.ClearStatus()
	wer.runAsync()
	return true
}

// Canceled 是否已经取消
func (wer *Worker) Canceled() bool {
	return wer.status.StatusCode() == StatusCodeCanceled
}

// Completed 是否已经完成
func (wer *Worker) Completed() bool {
	switch wer.status.StatusCode() {
	case StatusCodeSucceeded, StatusCodeCanceled:
		return true
	default:
		return false
	}
}

// Failed 是否失败
func (wer *Worker) Failed() bool {
	switch wer.status.StatusCode() {
	case StatusCodeFailed, StatusCodeInternalError, StatusCodeTooManyConnections, StatusCodeNetError:
		return true
	default:
		return false
	}
}

// ClearStatus 清空状态
func (wer *Worker) ClearStatus() {
	wer.status.SetStatusCode(StatusCodeInit)
}

// Err 返回worker错误
func (wer *Worker) Err() error {
	wer.errMu.RLock()
	defer wer.errMu.RUnlock()
	return wer.lastError
}

func (wer *Worker) setError(err error) {
	wer.errMu.Lock()
	wer.lastError = err
	wer.errMu.Unlock()
}

func (wer *Worker) runAsync() {
	wer.runWG.Add(1)
	go func() {
		defer wer.runWG.Done()
		wer.Execute()
	}()
}

// Execute 执行任务
func (wer *Worker) Execute() {
	wer.execMu.Lock()
	defer wer.execMu.Unlock()
	wer.lazyInit()
	wer.controlMu.Lock()
	if wer.paused {
		wer.status.SetStatusCode(StatusCodePaused)
		wer.controlMu.Unlock()
		return
	}
	pauseVersion := wer.pauseVersion
	cancelVersion := wer.cancelVersion
	wer.status.SetStatusCode(StatusCodeInit)
	wer.controlMu.Unlock()
	defer func() {
		wer.controlMu.Lock()
		defer wer.controlMu.Unlock()
		wer.pauseCancel = nil
		if wer.pauseVersion != pauseVersion && !wer.Completed() {
			if wer.paused {
				wer.status.SetStatusCode(StatusCodePaused)
			} else {
				wer.status.SetStatusCode(StatusCodeReseted)
			}
			wer.setError(nil)
		}
	}()
	single := wer.acceptRanges == ""

	if !single {
		// 已完成
		if rlen := wer.wrange.Len(); rlen <= 0 {
			if rlen < 0 {
				pcsverbose.Verbosef("DEBUG: RangeLen is negative at begin: %v, %d\n", wer.wrange, wer.wrange.Len())
			}
			wer.status.SetStatusCode(StatusCodeSucceeded)
			return
		}
	}

	parentCtx := wer.parentCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	workerCancelCtx, workerCancelFunc := context.WithCancel(parentCtx)
	defer workerCancelFunc()
	resetCtx, resetFunc := context.WithCancel(workerCancelCtx)
	wer.controlMu.Lock()
	wer.workerCancelFunc = workerCancelFunc
	wer.resetFunc = resetFunc
	wer.pauseCancel = resetFunc
	if wer.cancelVersion != cancelVersion {
		// A successful Cancel during context construction must not merely
		// cancel the preceding request's already completed context.
		workerCancelFunc()
	}
	if wer.paused || wer.pauseVersion != pauseVersion {
		resetFunc()
	}
	wer.controlMu.Unlock()
	defer resetFunc()
	if err := resetCtx.Err(); err != nil {
		wer.setError(err)
		wer.status.SetStatusCode(StatusCodeCanceled)
		if workerCancelCtx.Err() == nil {
			wer.status.SetStatusCode(StatusCodeReseted)
		}
		return
	}

	if single && wer.wrange.LoadBegin() != 0 {
		// Without Range support a new response starts at byte zero. A caller
		// must restart the whole transfer, not append that response to a prefix.
		if wer.firstResp != nil {
			if wer.firstResp.Body != nil {
				_ = wer.firstResp.Body.Close()
			}
			wer.firstResp = nil
		}
		wer.setError(errors.New("single stream cannot resume from a nonzero offset"))
		wer.status.SetStatusCode(StatusCodeInternalError)
		return
	}
	setInterruptedStatus := func() {
		err := workerCancelCtx.Err()
		wer.setError(err)
		if err != nil {
			wer.status.SetStatusCode(StatusCodeCanceled)
		} else {
			wer.status.SetStatusCode(StatusCodeReseted)
		}
	}
	header := map[string]string{}
	requestBegin, requestEnd := wer.wrange.LoadBegin(), wer.wrange.LoadEnd()
	if wer.referer != "" {
		header["Referer"] = wer.referer
	}
	//检测是否支持range
	if wer.acceptRanges != "" && wer.wrange.Len() >= 0 {
		header["Range"] = fmt.Sprintf("%s=%d-%d", wer.acceptRanges, requestBegin, requestEnd-1)
		if wer.etag != "" && !strings.HasPrefix(wer.etag, "W/") {
			header["If-Range"] = wer.etag
		} else if wer.etag == "" && wer.lastModified != "" {
			header["If-Range"] = wer.lastModified
		}
	}

	if err := wer.waitRetryAfter(resetCtx); err != nil {
		wer.setError(err)
		wer.status.SetStatusCode(StatusCodeCanceled)
		if workerCancelCtx.Err() == nil {
			wer.status.SetStatusCode(StatusCodeReseted)
		}
		return
	}
	wer.status.SetStatusCode(StatusCodePending)

	var resp *http.Response
	if wer.firstResp != nil {
		resp = wer.firstResp // 使用第一个连接
	} else {
		var err error
		resp, err = wer.client.ReqWithContext(resetCtx, http.MethodGet, wer.url, nil, header)
		wer.setError(err)
	}
	if resp != nil {
		// The reused URL-probe response was opened outside this worker context.
		// Close once, outside controlMu, and join any in-flight Close on return.
		closeBody := sync.OnceFunc(func() { resp.Body.Close() })
		stopClose := context.AfterFunc(resetCtx, closeBody)
		defer stopClose()
		defer func() {
			closeBody()
			wer.firstResp = nil // 去掉第一个连接
		}()
	}
	if wer.Err() != nil {
		wer.status.SetStatusCode(StatusCodeNetError)
		return
	}

	// 判断响应状态
	if resp.StatusCode == 429 || resp.StatusCode == 503 || resp.StatusCode == 509 {
		wer.setRetryAfter(resp.Header.Get("Retry-After"))
	}
	switch resp.StatusCode {
	case 200, 206:
		// do nothing, continue
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone,
		http.StatusRequestedRangeNotSatisfiable:
		// Repeating the same request cannot repair invalid parameters,
		// missing credentials, a rejected method, or a removed resource.
		wer.setError(fmt.Errorf("download rejected: HTTP status %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)))
		wer.status.SetStatusCode(StatusCodeInternalError)
		return
	case 406: // Not Acceptable
		wer.setError(errors.New(resp.Status))
		wer.status.SetStatusCode(StatusCodeNetError)
		return
	case 429, 509: // Too Many Requests
		wer.setError(errors.New(resp.Status))
		wer.status.SetStatusCode(StatusCodeTooManyConnections)
		return
	default:
		wer.setError(fmt.Errorf("unexpected http status code, %d, %s", resp.StatusCode, resp.Status))
		wer.status.SetStatusCode(StatusCodeNetError)
		return
	}
	if single && resp.StatusCode == http.StatusPartialContent {
		wer.setError(errors.New("unexpected partial response for full-stream download"))
		wer.status.SetStatusCode(StatusCodeInternalError)
		return
	}
	// Only successful file responses describe the entity being downloaded.
	// A throttling/authentication/error document must retain its HTTP error
	// classification and must not invalidate saved ranges through its ETag.
	if (wer.etag != "" && resp.Header.Get("ETag") != wer.etag) || (wer.etag == "" && wer.lastModified != "" && resp.Header.Get("Last-Modified") != wer.lastModified) {
		wer.setError(ErrRemoteChanged)
		wer.status.SetStatusCode(StatusCodeInternalError)
		return
	}

	var (
		contentLength = resp.ContentLength
		rangeLength   = wer.wrange.Len()
	)

	if !single {
		// An initial URL probe supplies a prefix from offset zero. Subsequent
		// ranged responses must describe the exact interval sent in the request.
		initialFull := wer.firstResp != nil && resp.StatusCode == http.StatusOK
		expectedLength := requestEnd - requestBegin
		if initialFull {
			if requestBegin != 0 || contentLength != wer.totalSize || contentLength < requestEnd {
				wer.setError(errors.New("initial full response does not cover requested range"))
				wer.status.SetStatusCode(StatusCodeInternalError)
				return
			}
		} else {
			begin, end, total, ok := parseContentRangeBounds(resp.Header.Get("Content-Range"))
			if resp.StatusCode != http.StatusPartialContent || !ok || begin != requestBegin || (wer.firstResp == nil && end != requestEnd-1) || (wer.firstResp != nil && requestBegin != 0) || (wer.totalSize > 0 && total != wer.totalSize) {
				wer.setError(fmt.Errorf("unexpected Content-Range %q for bytes %d-%d/%d", resp.Header.Get("Content-Range"), requestBegin, requestEnd-1, wer.totalSize))
				wer.status.SetStatusCode(StatusCodeInternalError)
				return
			}
			// The URL probe may return only a prefix of the first block. Its
			// own Content-Range must agree with its body length; the monitor
			// can request the remainder after this response is consumed.
			if wer.firstResp != nil {
				expectedLength = end - begin + 1
			}
		}
		// Chunked responses have no Content-Length. Their validated
		// Content-Range still bounds the write; early EOF leaves bytes pending.
		if contentLength >= 0 && contentLength != expectedLength && !initialFull {
			wer.setError(fmt.Errorf("Content-Length is unexpected: %d, need %d", contentLength, expectedLength))
			wer.status.SetStatusCode(StatusCodeNetError)
			return
		}
	}

	var (
		buf       = cachepool.GetBuffer(wer.cacheSize)
		n, nn     int
		n64, nn64 int64
	)
	defer cachepool.PutBuffer(buf)

	for {
		select {
		case <-workerCancelCtx.Done(): //取消
			wer.status.SetStatusCode(StatusCodeCanceled)
			return
		case <-resetCtx.Done(): //重设连接
			wer.status.SetStatusCode(StatusCodeReseted)
			return
		default:
			wer.status.SetStatusCode(StatusCodeDownloading)

			// 初始化数据
			var readErr error
			n = 0

			// 读取数据
			for n < len(buf) && readErr == nil && (single || wer.wrange.Len() > 0) {
				nn, readErr = resp.Body.Read(buf[n:])
				nn64 = int64(nn)

				// 更新速度统计
				if wer.downloadStatus != nil {
					if wer.downloadStatus.HasRateLimit() {
						wer.status.SetStatusCode(StatusCodeRateLimited)
					}
					if err := wer.downloadStatus.AddSpeedsDownloadedWithContext(resetCtx, nn64); err != nil {
						setInterruptedStatus()
						return
					}
					wer.status.SetStatusCode(StatusCodeDownloading)
				}
				wer.speedsStat.Add(nn64)
				n += nn
			}

			n64 = int64(n)
			if wer.writerAt != nil {
				wer.status.SetStatusCode(StatusCodeWaitToWrite)
			}
			// Splitting and committing a write share this lock. Recheck the
			// range after acquiring it: buffered data may predate a split.
			if wer.writeMu != nil {
				wer.writeMu.Lock()
			}
			// A control request can arrive while this buffered write waits behind
			// another writer. Do not commit bytes from the canceled execution.
			if resetCtx.Err() != nil {
				if wer.writeMu != nil {
					wer.writeMu.Unlock()
				}
				setInterruptedStatus()
				return
			}

			// 非单线程模式下
			if !single {
				rangeLength = wer.wrange.Len()

				// 已完成 (未雨绸缪)
				if rangeLength <= 0 {
					if wer.writeMu != nil {
						wer.writeMu.Unlock()
					}
					wer.setError(errors.New("worker already complete"))
					wer.status.SetStatusCode(StatusCodeCanceled)
					return
				}

				if n64 > rangeLength {
					// 数据大小不正常
					n64 = rangeLength
					n = int(rangeLength)
					readErr = io.EOF
				}
			}

			// 写入数据
			if wer.writerAt != nil {
				written, writeErr := wer.writerAt.WriteAt(buf[:n], wer.wrange.LoadBegin())
				if writeErr == nil && written != n {
					writeErr = io.ErrShortWrite
				}
				wer.setError(writeErr)
				if writeErr != nil {
					if wer.writeMu != nil {
						wer.writeMu.Unlock() //解锁
					}
					wer.status.SetStatusCode(StatusCodeInternalError)
					return
				}
			}

			// 更新下载统计数据
			wer.wrange.AddBegin(n64)
			if wer.downloadStatus != nil {
				wer.downloadStatus.AddDownloaded(n64)
				if single && wer.totalSize < 0 {
					wer.downloadStatus.AddTotalSize(n64)
				}
			}
			if wer.writeMu != nil {
				wer.writeMu.Unlock()
			}
			if wer.writerAt != nil {
				wer.status.SetStatusCode(StatusCodeDownloading)
			}
			// A write already in progress must finish and advance its checkpoint,
			// but cancellation during that write must not become success.
			if resetCtx.Err() != nil {
				setInterruptedStatus()
				return
			}

			if !single && wer.wrange.Len() <= 0 {
				wer.status.SetStatusCode(StatusCodeSucceeded)
				return
			}
			if readErr != nil {
				if single && readErr == io.EOF {
					wer.status.SetStatusCode(StatusCodeSucceeded)
					return
				}
				// A range may outlive the initial prefix response. Its EOF must
				// leave the remainder pending rather than certify missing bytes.
				if readErr == io.EOF {
					readErr = io.ErrUnexpectedEOF
				}
				wer.setError(readErr)
				if single {
					// A full stream cannot resume at the current write offset:
					// return the error so the enclosing task can restart it.
					wer.status.SetStatusCode(StatusCodeInternalError)
				} else {
					wer.status.SetStatusCode(StatusCodeFailed)
				}
				return
			}
		}
	}
}
