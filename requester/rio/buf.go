package rio

import (
	"errors"
	"io"
	"unsafe"
)

// Buffer 为固定长度的 Buf, 实现 io.WriterAt 接口
type Buffer struct {
	Buf []byte
}

// NewBuffer 初始化 Buffer
func NewBuffer(buf []byte) *Buffer {
	return &Buffer{
		Buf: buf,
	}
}

// ReadAt 实现 io.ReadAt 接口
func (b *Buffer) ReadAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errors.New("rio: negative buffer offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= int64(len(b.Buf)) {
		return 0, io.EOF
	}
	n = copy(p, b.Buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// WriteAt 实现 io.WriterAt 接口
func (b *Buffer) WriteAt(p []byte, off int64) (n int, err error) {
	if off < 0 {
		return 0, errors.New("rio: negative buffer offset")
	}
	if len(p) == 0 {
		return 0, nil
	}
	if off >= int64(len(b.Buf)) {
		return 0, io.ErrShortWrite
	}
	n = copy(b.Buf[off:], p)
	if n < len(p) {
		return n, io.ErrShortWrite
	}
	return n, nil
}

// Bytes 返回 buf
func (b *Buffer) Bytes() []byte {
	return b.Buf
}

func (b *Buffer) String() string {
	return *(*string)(unsafe.Pointer(&b.Buf))
}
