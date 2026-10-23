// Package multipartreader helps you encode large files in MIME multipart format
// without reading the entire content into memory.
package multipartreader

import (
	"errors"
	"fmt"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"io"
	"mime/multipart"
	"strings"
	"sync"
	"sync/atomic"
)

var headerParameterEscaper = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\r", "%0D", "\n", "%0A")

type (
	// MultipartReader MIME multipart format
	MultipartReader struct {
		length      int64
		contentType string
		boundary    string

		formBody  string
		parts     []*part
		part64s   []*part64
		formClose string

		mu          sync.Mutex
		closed      bool
		multiReader io.Reader
	}

	part struct {
		size      int64
		form      string
		readerlen rio.ReaderLen
	}

	part64 struct {
		size        int64
		form        string
		readerlen64 rio.ReaderLen64
	}
)

// NewMultipartReader 返回初始化的 *MultipartReader
func NewMultipartReader() (mr *MultipartReader) {
	builder := &strings.Builder{}
	writer := multipart.NewWriter(builder)
	mr = &MultipartReader{
		contentType: writer.FormDataContentType(),
		boundary:    writer.Boundary(),
	}

	mr.length += int64(builder.Len())
	mr.formBody = builder.String()
	return
}

// AddFormField 增加 form 表单。封装完成后忽略新增字段。
func (mr *MultipartReader) AddFormField(fieldname string, readerlen rio.ReaderLen) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if mr.closed {
		return
	}
	if readerlen == nil {
		return
	}

	mpart := &part{
		size:      int64(readerlen.Len()),
		form:      fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n", mr.boundary, headerParameterEscaper.Replace(fieldname)),
		readerlen: readerlen,
	}
	atomic.AddInt64(&mr.length, int64(len(mpart.form))+mpart.size)
	mr.parts = append(mr.parts, mpart)
}

// AddFormFile 增加 form 文件表单。封装完成后忽略新增字段。
func (mr *MultipartReader) AddFormFile(fieldname, filename string, readerlen64 rio.ReaderLen64) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if mr.closed {
		return
	}
	if readerlen64 == nil {
		return
	}

	mpart64 := &part64{
		size:        readerlen64.Len(),
		form:        fmt.Sprintf("--%s\r\nContent-Disposition: form-data; name=\"%s\"; filename=\"%s\"\r\n\r\n", mr.boundary, headerParameterEscaper.Replace(fieldname), headerParameterEscaper.Replace(filename)),
		readerlen64: readerlen64,
	}
	atomic.AddInt64(&mr.length, int64(len(mpart64.form))+mpart64.size)
	mr.part64s = append(mr.part64s, mpart64)
}

// CloseMultipart 关闭multipartreader
func (mr *MultipartReader) CloseMultipart() error {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if mr.closed {
		return errors.New("multipartreader already closed")
	}
	mr.formClose = "\r\n--" + mr.boundary + "--\r\n"
	total := int64(len(mr.formBody))
	add := func(size int64) bool {
		if size < 0 || size > (1<<63-1)-total {
			return false
		}
		total += size
		return true
	}
	count := 0
	addPart := func(header string, size int64) bool {
		if count > 0 && !add(2) {
			return false
		}
		count++
		return add(int64(len(header))) && add(size)
	}
	for _, part := range mr.parts {
		if !addPart(part.form, part.size) {
			return errors.New("multipartreader: invalid or overflowing length")
		}
	}
	for _, part := range mr.part64s {
		if !addPart(part.form, part.size) {
			return errors.New("multipartreader: invalid or overflowing length")
		}
	}
	if !add(int64(len(mr.formClose))) {
		return errors.New("multipartreader: overflowing length")
	}

	numReaders := 0
	if mr.formBody != "" {
		numReaders++
	}
	numReaders += 2*len(mr.parts) + 2*len(mr.part64s)
	if mr.formClose != "" {
		numReaders++
	}

	readers := make([]io.Reader, 0, numReaders)
	readers = append(readers, strings.NewReader(mr.formBody))
	partCount := 0
	separator := func() {
		if partCount > 0 {
			readers = append(readers, strings.NewReader("\r\n"))
		}
		partCount++
	}
	for k := range mr.parts {
		separator()
		readers = append(readers, strings.NewReader(mr.parts[k].form), &exactPartReader{io.LimitedReader{R: mr.parts[k].readerlen, N: mr.parts[k].size}})
	}
	for k := range mr.part64s {
		separator()
		readers = append(readers, strings.NewReader(mr.part64s[k].form), &exactPartReader{io.LimitedReader{R: mr.part64s[k].readerlen64, N: mr.part64s[k].size}})
	}
	readers = append(readers, strings.NewReader(mr.formClose))
	mr.multiReader = io.MultiReader(readers...)
	atomic.StoreInt64(&mr.length, total)

	mr.closed = true
	return nil
}

// ContentType 返回Content-Type
func (mr *MultipartReader) ContentType() string {
	return mr.contentType
}

func (mr *MultipartReader) Read(p []byte) (n int, err error) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	if !mr.closed {
		return 0, errors.New("multipartreader not closed")
	}
	n, err = mr.multiReader.Read(p)
	return n, err
}

// Len 返回表单内容总长度
func (mr *MultipartReader) Len() int64 {
	return atomic.LoadInt64(&mr.length)
}
