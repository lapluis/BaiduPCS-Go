package uploader

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type terminalRangeSource struct {
	*bytes.Reader
	end     int64
	failure error
}

func (r terminalRangeSource) ReadAt(p []byte, off int64) (int, error) {
	n, err := r.Reader.ReadAt(p, off)
	if n == len(p) && off+int64(n) == r.end {
		return n, r.failure
	}
	return n, err
}

type cappedUploadReader struct {
	io.Reader
	chunk int
}

func (r cappedUploadReader) Read(p []byte) (int, error) {
	if len(p) > r.chunk {
		p = p[:r.chunk]
	}
	return r.Reader.Read(p)
}

func FuzzBufferedUploadTerminalError(f *testing.F) {
	for _, size := range []int{0, 1, 4, BufioReadSize - 1, BufioReadSize, BufioReadSize + 1, 2*BufioReadSize + 1} {
		data := make([]byte, size+8)
		for i := range data {
			data[i] = byte(i*31 + i/257)
		}
		for _, chunk := range []uint32{1, 511, uint32(BufioReadSize), uint32(2 * BufioReadSize)} {
			f.Add(data, uint32(3), uint32(size), chunk)
		}
	}
	f.Fuzz(func(t *testing.T, data []byte, rawBegin, rawLength, rawChunk uint32) {
		if len(data) > 2*BufioReadSize+16 {
			t.Skip()
		}
		begin := int(uint64(rawBegin) % uint64(len(data)+1))
		length := int(uint64(rawLength) % uint64(len(data)-begin+1))
		chunk := 1 + int(rawChunk%uint32(2*BufioReadSize))
		cause := errors.New("fixture terminal source failure")
		for _, terminal := range []error{cause, io.EOF, nil} {
			source := terminalRangeSource{bytes.NewReader(data), int64(begin + length), terminal}
			unit := NewBufioSplitUnit(source, transfer.Range{Begin: int64(begin), End: int64(begin + length)}, nil, nil)
			// A declared-length consumer never asks the unit for another byte.
			// Compare the actual bytes to an independent slice of the source.
			reader := io.LimitReader(cappedUploadReader{unit, chunk}, int64(length))
			got, err := io.ReadAll(reader)
			if !bytes.Equal(got, data[begin:begin+length]) {
				t.Fatalf("range content changed: begin=%d length=%d chunk=%d got=%d", begin, length, chunk, len(got))
			}
			if terminal == cause && length > 0 {
				if !errors.Is(err, cause) || !errors.Is(err, ErrSourceRead) {
					t.Fatalf("terminal failure lost: begin=%d length=%d chunk=%d err=%v", begin, length, chunk, err)
				}
			} else if err != nil {
				t.Fatalf("healthy or empty range rejected: %v", err)
			}
		}
	})
}
