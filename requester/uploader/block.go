package uploader

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio/speeds"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"io"
	"os"
	"sync"
	"sync/atomic"
)

type (
	// SplitUnit 将 io.ReaderAt 分割单元
	SplitUnit interface {
		Readed64
		io.Seeker
		Range() transfer.Range
		Left() int64
	}

	fileBlock struct {
		readRange     transfer.Range
		readed        atomic.Int64
		readerAt      io.ReaderAt
		speedsStatRef *speeds.Speeds
		rateLimit     *speeds.RateLimit
		readCtx       context.Context
		mu            sync.Mutex
	}

	bufioFileBlock struct {
		*fileBlock
		bufio *bufio.Reader
	}
)

// SplitBlock 文件分块
func SplitBlock(fileSize, blockSize int64) (blockList []*BlockState) {
	gen := transfer.NewRangeListGenBlockSize(fileSize, 0, blockSize)
	rangeCount := gen.RangeCount()
	blockList = make([]*BlockState, 0, rangeCount)
	for i := 0; i < rangeCount; i++ {
		id, r := gen.GenRange()
		blockList = append(blockList, &BlockState{
			ID:    id,
			Range: *r,
		})
	}
	return
}

// NewBufioSplitUnit io.ReaderAt实现SplitUnit接口, 有Buffer支持
func NewBufioSplitUnit(readerAt io.ReaderAt, readRange transfer.Range, speedsStat *speeds.Speeds, rateLimit *speeds.RateLimit) SplitUnit {
	su := &fileBlock{
		readerAt:      readerAt,
		readRange:     readRange,
		speedsStatRef: speedsStat,
		rateLimit:     rateLimit,
	}
	return &bufioFileBlock{
		fileBlock: su,
		bufio:     bufio.NewReaderSize(su, BufioReadSize),
	}
}

func (bfb *bufioFileBlock) Read(b []byte) (n int, err error) {
	return bfb.bufio.Read(b) // 间接调用fileBlock 的Read
}

func (bfb *bufioFileBlock) Seek(offset int64, whence int) (int64, error) {
	if whence == io.SeekCurrent {
		buffered := int64(bfb.bufio.Buffered())
		if offset < (-1<<63)+buffered {
			return 0, fmt.Errorf("seek offset overflow")
		}
		offset -= buffered
	}
	position, err := bfb.fileBlock.Seek(offset, whence)
	if err == nil {
		bfb.bufio.Reset(bfb.fileBlock)
	}
	return position, err
}

// Read 只允许一个线程读同一个文件
func (fb *fileBlock) Read(b []byte) (n int, err error) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	left := fb.Left()
	if left <= 0 {
		return 0, io.EOF
	}

	if int64(len(b)) > left {
		b = b[:left]
	}
	offset := fb.readed.Load() + fb.readRange.Begin
	n, err = fb.readerAt.ReadAt(b, offset)

	n64 := int64(n)
	if err == nil && n < len(b) {
		err = io.ErrUnexpectedEOF
	}
	if err != nil && !(err == io.EOF && n64 == left) {
		err = fmt.Errorf("%w at offset %d: %w", ErrSourceRead, offset, err)
	}
	fb.readed.Add(n64)
	if fb.rateLimit != nil && !errors.Is(err, ErrSourceRead) {
		ctx := fb.readCtx
		if ctx == nil {
			ctx = context.Background()
		}
		if limitErr := fb.rateLimit.AddWithContext(ctx, n64); limitErr != nil {
			return 0, limitErr
		}
	}
	if fb.speedsStatRef != nil {
		fb.speedsStatRef.Add(n64)
	}
	return
}

func (fb *fileBlock) setContext(ctx context.Context) {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	fb.readCtx = ctx
}

func (fb *fileBlock) Seek(offset int64, whence int) (int64, error) {
	fb.mu.Lock()
	defer fb.mu.Unlock()

	var base int64
	switch whence {
	case os.SEEK_SET:
		base = 0
	case os.SEEK_CUR:
		base = fb.readed.Load()
	case os.SEEK_END:
		base = fb.readRange.End - fb.readRange.Begin
	default:
		return 0, fmt.Errorf("unsupport whence: %d", whence)
	}
	if base < 0 || offset < -base || offset > (1<<63-1)-base {
		return 0, fmt.Errorf("invalid seek position")
	}
	position := base + offset
	fb.readed.Store(position)
	return position, nil
}

func (fb *fileBlock) Len() int64 {
	return fb.readRange.End - fb.readRange.Begin
}

func (fb *fileBlock) Left() int64 {
	return fb.readRange.End - fb.readRange.Begin - fb.readed.Load()
}

func (fb *fileBlock) Range() transfer.Range {
	return fb.readRange
}

func (fb *fileBlock) Readed() int64 {
	return fb.readed.Load()
}
