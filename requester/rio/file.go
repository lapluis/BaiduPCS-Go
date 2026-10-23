package rio

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
)

// ErrSourceChanged indicates that the file no longer matches the reader's snapshot.
var ErrSourceChanged = errors.New("source file changed")

// SnapshotValidator lets transfer code reject files modified during a transfer.
type SnapshotValidator interface{ ValidateSnapshot() error }

type (
	fileReadedlen64 struct {
		readed      int64
		f           *os.File
		snapshot    os.FileInfo
		snapshotErr error
	}

	rdReadedlen64 struct {
		readed int64
		size   int64
		rd     io.Reader
	}
)

// NewFileReaderLen64 *os.File 实现 ReadedLen64 接口
func NewFileReaderLen64(f *os.File) ReaderLen64 {
	if f == nil {
		return nil
	}

	position, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		position = 0
	}
	snapshot, snapshotErr := f.Stat()
	return &fileReadedlen64{
		f:           f,
		readed:      position,
		snapshot:    snapshot,
		snapshotErr: snapshotErr,
	}
}

// NewFileReaderAtLen64 *os.File 实现 ReaderAtLen64 接口
func NewFileReaderAtLen64(f *os.File) ReaderAtLen64 {
	if f == nil {
		return nil
	}

	snapshot, err := f.Stat()
	return &fileReadedlen64{f: f, snapshot: snapshot, snapshotErr: err}
}

func (fr *fileReadedlen64) ValidateSnapshot() error {
	if fr.snapshotErr != nil {
		return fmt.Errorf("%w: %w", ErrSourceChanged, fr.snapshotErr)
	}
	info, err := fr.f.Stat()
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSourceChanged, err)
	}
	if fr.snapshot == nil || !os.SameFile(fr.snapshot, info) || fr.snapshot.Size() != info.Size() || !fr.snapshot.ModTime().Equal(info.ModTime()) {
		return ErrSourceChanged
	}
	return nil
}

func NewCryptoRandReaderAtLen64(size int64) ReaderAtLen64 {
	return &rdReadedlen64{
		rd:   cryptorand.Reader,
		size: size,
	}
}

// Read 读文件, 并记录已读取数据量
func (fr *fileReadedlen64) Read(b []byte) (n int, err error) {
	n, err = fr.f.Read(b)
	atomic.AddInt64(&fr.readed, int64(n))
	return n, err
}

// ReadAt 读文件, 不记录已读取数据量
func (fr *fileReadedlen64) ReadAt(b []byte, off int64) (n int, err error) {
	n, err = fr.f.ReadAt(b, off)
	return n, err
}

// Len 返回文件的大小
func (fr *fileReadedlen64) Len() int64 {
	info, err := fr.f.Stat()
	if err != nil {
		return 0
	}
	remaining := info.Size() - atomic.LoadInt64(&fr.readed)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (rr *rdReadedlen64) Read(b []byte) (n int, err error) {
	n, err = rr.ReadAt(b, 0)
	atomic.AddInt64(&rr.readed, int64(n))
	return n, err
}

func (rr *rdReadedlen64) ReadAt(b []byte, off int64) (n int, err error) {
	n, err = rr.rd.Read(b)
	return n, err
}

func (rr *rdReadedlen64) Len() int64 {
	return rr.size - atomic.LoadInt64(&rr.readed)
}
