// Package checksum 校验本地文件包
package checksum

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/cachepool"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/converter"
	"hash/crc32"
	"io"
	"os"
	"runtime"
	"sync"
)

const (
	// DefaultBufSize 默认的bufSize
	DefaultBufSize = int(1 * converter.MB)
)

const (
	// CHECKSUM_MD5 获取文件的 md5 值
	CHECKSUM_MD5 int = 1 << iota
	// CHECKSUM_SLICE_MD5 获取文件前 sliceSize 切片的 md5 值
	CHECKSUM_SLICE_MD5
	// CHECKSUM_CRC32 获取文件的 crc32 值
	CHECKSUM_CRC32
)

type (
	// LocalFileMeta 本地文件元信息
	LocalFileMeta struct {
		Path       string   `json:"path"`      // 本地路径
		Length     int64    `json:"length"`    // 文件大小
		SliceMD5   []byte   `json:"slicemd5"`  // 文件前 requiredSliceLen (256KB) 切片的 md5 值
		BlocksList []string `json:"blocklist"` // 文件分块计算md5的切片
		MD5        []byte   `json:"md5"`       // 文件的 md5
		CRC32      uint32   `json:"crc32"`     // 文件的 crc32
		ModTime    int64    `json:"modtime"`   // 修改日期
	}

	// LocalFileChecksum 校验本地文件
	LocalFileChecksum struct {
		LocalFileMeta
		bufSize   int
		sliceSize int
		buf       []byte
		file      *os.File // 文件
	}
)

func NewLocalFileChecksum(localPath string, sliceSize int) *LocalFileChecksum {
	return NewLocalFileChecksumWithBufSize(localPath, DefaultBufSize, sliceSize)
}

func NewLocalFileChecksumWithBufSize(localPath string, bufSize, sliceSize int) *LocalFileChecksum {
	return &LocalFileChecksum{
		LocalFileMeta: LocalFileMeta{
			Path: localPath,
		},
		bufSize:   bufSize,
		sliceSize: sliceSize,
	}
}

// OpenPath 检查文件状态并获取文件的大小 (Length)
func (lfc *LocalFileChecksum) OpenPath() error {
	if lfc.file != nil {
		lfc.file.Close()
		lfc.file = nil
	}
	// Digests belong to the previous open, even if this path now has the same
	// size and timestamp. Resume metadata can be restored after validation.
	lfc.LocalFileMeta = LocalFileMeta{Path: lfc.Path}

	file, err := os.Open(lfc.Path)
	if err != nil {
		return err
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}

	lfc.file = file
	lfc.Length = info.Size()
	lfc.ModTime = info.ModTime().Unix()
	return nil
}

// GetFile 获取文件
func (lfc *LocalFileChecksum) GetFile() *os.File {
	return lfc.file
}

// Close 关闭文件
func (lfc *LocalFileChecksum) Close() error {
	if lfc.file == nil {
		return ErrFileIsNil
	}

	return lfc.file.Close()
}

func (lfc *LocalFileChecksum) initBuf() {
	if lfc.buf == nil {
		lfc.buf = cachepool.RawMallocByteSlice(lfc.bufSize)
	}
}

func (lfc *LocalFileChecksum) writeChecksum(data []byte, wus ...*ChecksumWriteUnit) (err error) {
	doneCount := 0
	for _, wu := range wus {
		_, err := wu.Write(data)
		switch err {
		case ErrChecksumWriteStop:
			doneCount++
			continue
		case nil:
		default:
			return err
		}
	}
	if doneCount == len(wus) {
		return ErrChecksumWriteAllStop
	}
	return nil
}

func (lfc *LocalFileChecksum) GetSliceDataContent(offset, length int64) (dataContent []byte, readLength int64, err error) {
	dataContent = make([]byte, length)
	ret, err := lfc.file.ReadAt(dataContent, offset)
	if err != nil && err != io.EOF {
		return
	}
	readLength = int64(ret)
	dataContent = dataContent[:ret]
	return dataContent, readLength, nil
}

func (lfc *LocalFileChecksum) repeatRead(wus ...*ChecksumWriteUnit) (err error) {
	if lfc.file == nil {
		return ErrFileIsNil
	}

	lfc.initBuf()

	defer func() {
		if _, seekErr := lfc.file.Seek(0, io.SeekStart); seekErr != nil {
			err = errors.Join(err, seekErr)
		}
	}()
	if _, err = lfc.file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	for {
		n, readErr := lfc.file.Read(lfc.buf)
		if readErr != nil && readErr != io.EOF {
			return readErr
		}
		writeErr := lfc.writeChecksum(lfc.buf[:n], wus...)
		if writeErr == ErrChecksumWriteAllStop {
			return nil
		}
		if writeErr != nil {
			return writeErr
		}
		if readErr == io.EOF {
			// An exact boundary may require a final empty write to publish the
			// completed digest. An unfinished unit means the file ended early.
			writeErr = lfc.writeChecksum(nil, wus...)
			if writeErr == ErrChecksumWriteAllStop {
				return nil
			}
			if writeErr != nil {
				return writeErr
			}
			return io.ErrUnexpectedEOF
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}
}

func (lfc *LocalFileChecksum) createChecksumWriteUnit(cw ChecksumWriter, isAll, isSlice bool, getSumFunc func(sliceSum interface{}, sum interface{})) (wu *ChecksumWriteUnit, deferFunc func(err error)) {
	wu = &ChecksumWriteUnit{
		ChecksumWriter: cw,
		End:            lfc.LocalFileMeta.Length,
		OnlySliceSum:   !isAll,
	}

	if isSlice {
		wu.SliceEnd = int64(lfc.sliceSize)
	}

	return wu, func(err error) {
		if err != nil {
			return
		}
		getSumFunc(wu.SliceSum, wu.Sum)
	}
}

// Sum 计算文件摘要值
func (lfc *LocalFileChecksum) Sum(checkSumFlag int) (err error) {
	lfc.fix()
	wus := make([]*ChecksumWriteUnit, 0, 2)
	if (checkSumFlag & (CHECKSUM_MD5 | CHECKSUM_SLICE_MD5)) != 0 {
		md5w := md5.New()
		wu, d := lfc.createChecksumWriteUnit(
			NewHashChecksumWriter(md5w),
			(checkSumFlag&CHECKSUM_MD5) != 0,
			(checkSumFlag&CHECKSUM_SLICE_MD5) != 0,
			func(sliceSum interface{}, sum interface{}) {
				if sliceSum != nil {
					lfc.SliceMD5 = sliceSum.([]byte)
				}
				if sum != nil {
					lfc.MD5 = sum.([]byte)
				}
			},
		)

		wus = append(wus, wu)
		defer func() { d(err) }()
	}
	if (checkSumFlag & CHECKSUM_CRC32) != 0 {
		crc32w := crc32.NewIEEE()
		wu, d := lfc.createChecksumWriteUnit(
			NewHash32ChecksumWriter(crc32w),
			true,
			false,
			func(sliceSum interface{}, sum interface{}) {
				if sum != nil {
					lfc.CRC32 = sum.(uint32)
				}
			},
		)

		wus = append(wus, wu)
		defer func() { d(err) }()
	}

	err = lfc.repeatRead(wus...)
	return
}

// CalculateChunkedSum 按指定大小分块并行计算MD5
func (lfc *LocalFileChecksum) CalculateChunkedSum(chunkSize int64) (err error) {

	// 确保分块大小有效
	if chunkSize <= 0 {
		return fmt.Errorf("invalid block size: %d", chunkSize)
	}
	if lfc.file == nil {
		return ErrFileIsNil
	}

	fileSize := lfc.Length
	if fileSize < 0 {
		return fmt.Errorf("invalid file size: %d", fileSize)
	}
	checkSize := func() error {
		info, err := lfc.file.Stat()
		if err != nil {
			return err
		}
		if info.Size() < fileSize {
			return fmt.Errorf("file shrank from %d to %d bytes: %w", fileSize, info.Size(), io.ErrUnexpectedEOF)
		}
		if info.Size() > fileSize {
			return fmt.Errorf("file size changed from %d to %d bytes", fileSize, info.Size())
		}
		return nil
	}
	if err := checkSize(); err != nil {
		return err
	}
	if fileSize == 0 {
		lfc.BlocksList = nil
		return nil // 空文件
	}

	// 计算分块数量
	count := (fileSize-1)/chunkSize + 1
	if count > int64(int(^uint(0)>>1)) {
		return fmt.Errorf("too many checksum blocks: %d", count)
	}
	chunkCount := int(count)

	// 初始化结果存储, 各 worker 按自己的块索引写入, 无需加锁
	blocks := make([]string, chunkCount)

	// worker 数: 不超过逻辑核数, 不超过分块数, 上限 16 (控制 4MB×N 的缓冲区内存)
	workers := runtime.GOMAXPROCS(0)
	if workers > 16 {
		workers = 16
	}
	if workers > chunkCount {
		workers = chunkCount
	}

	var wg sync.WaitGroup
	var failureOnce sync.Once
	var firstErr error
	stopped := make(chan struct{})
	fail := func(err error) { failureOnce.Do(func() { firstErr = err; close(stopped) }) }
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerId int) {
			defer wg.Done()

			buffer := make([]byte, 4*converter.MB) // 每个 worker 独享缓冲区, 不可共享
			chunkMD5 := md5.New()

			for i := workerId; i < chunkCount; i += workers {
				select {
				case <-stopped:
					return
				default:
				}
				offset := int64(i) * chunkSize
				// 计算当前分块的实际大小（最后一块可能较小）
				currentChunkSize := min(chunkSize, fileSize-offset)

				chunkMD5.Reset()
				bytesRead := int64(0)

				// 读取当前分块的所有数据
				for bytesRead < currentChunkSize {
					select {
					case <-stopped:
						return
					default:
					}
					readSize := int64(len(buffer))
					if readSize > currentChunkSize-bytesRead {
						readSize = currentChunkSize - bytesRead
					}

					n, rerr := lfc.file.ReadAt(buffer[:readSize], offset+bytesRead)
					if n > 0 {
						chunkMD5.Write(buffer[:n])
						bytesRead += int64(n)
					}
					if rerr == io.EOF && bytesRead < currentChunkSize {
						rerr = io.ErrUnexpectedEOF
					}
					if rerr != nil && rerr != io.EOF {
						fail(rerr)
						return
					}
					if n == 0 {
						fail(io.ErrNoProgress)
						return
					}
				}

				blocks[i] = hex.EncodeToString(chunkMD5.Sum(nil))
			}
		}(w)
	}
	wg.Wait()

	if firstErr != nil {
		return firstErr
	}
	if err := checkSize(); err != nil {
		return err
	}
	lfc.BlocksList = blocks
	return nil
}

func (lfc *LocalFileChecksum) fix() {
	if lfc.sliceSize <= 0 {
		lfc.sliceSize = DefaultBufSize
	}
	if lfc.bufSize < DefaultBufSize {
		lfc.bufSize = DefaultBufSize
	}
}
