package multipartreader

import "io"

// exactPartReader stops at the declared size and prevents a truncated part
// from silently falling through to the next multipart header.
type exactPartReader struct{ io.LimitedReader }

func (r *exactPartReader) Read(p []byte) (int, error) {
	n, err := r.LimitedReader.Read(p)
	if err == io.EOF && r.N > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
