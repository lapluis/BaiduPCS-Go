package checksum

import (
	"bytes"
	"crypto/md5"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestSumPreservesFileReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write-only.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err = file.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	checksum.file, checksum.Length = file, 7
	err = checksum.Sum(CHECKSUM_MD5)
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "read" {
		t.Fatalf("file read error lost: %v", err)
	}
	if offset, err := file.Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("position not restored: %d, %v", offset, err)
	}
}

func TestSumRejectsPrematureEOFWithoutPublishingPartialDigests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truncated.bin")
	if err := os.WriteFile(path, bytes.Repeat([]byte("a"), DefaultBufSize+8), 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	if err := os.Truncate(path, int64(DefaultBufSize+4)); err != nil {
		t.Fatal(err)
	}
	err := checksum.Sum(CHECKSUM_MD5 | CHECKSUM_SLICE_MD5 | CHECKSUM_CRC32)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("premature EOF reported as %v", err)
	}
	if checksum.MD5 != nil || checksum.SliceMD5 != nil || checksum.CRC32 != 0 {
		t.Fatal("failed checksum published partial digests")
	}
}

type failingChecksumWriter struct {
	err   error
	calls int
}

func (w *failingChecksumWriter) Write([]byte) (int, error) { w.calls++; return 0, w.err }
func (w *failingChecksumWriter) Sum() interface{}          { return []byte("invalid") }

func TestRepeatReadPreservesChecksumWriterError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	for _, writerErr := range []error{errors.New("fixture checksum writer failure"), nil} {
		wantErr := writerErr
		if wantErr == nil {
			wantErr = io.ErrShortWrite
		}
		writer := &failingChecksumWriter{err: writerErr}
		err := checksum.repeatRead(&ChecksumWriteUnit{End: checksum.Length, ChecksumWriter: writer})
		if !errors.Is(err, wantErr) {
			t.Fatalf("writer error lost: %v", err)
		}
		if writer.calls != 1 {
			t.Fatalf("continued writing after failure: %d calls", writer.calls)
		}
	}
}

func TestSumStartsAtBeginningAndRestoresPosition(t *testing.T) {
	data := []byte("checksum fixture")
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	if _, err := checksum.GetFile().Seek(5, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := checksum.Sum(CHECKSUM_MD5); err != nil {
		t.Fatal(err)
	}
	want := md5.Sum(data)
	if !bytes.Equal(checksum.MD5, want[:]) {
		t.Fatal("digest omitted the start of the file")
	}
	if offset, err := checksum.GetFile().Seek(0, io.SeekCurrent); err != nil || offset != 0 {
		t.Fatalf("position not restored: %d, %v", offset, err)
	}
}

func TestSumCompleteDigestsAtBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 4, 5, DefaultBufSize, DefaultBufSize + 1} {
		data := bytes.Repeat([]byte("x"), size)
		path := filepath.Join(t.TempDir(), "fixture.bin")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		checksum := NewLocalFileChecksum(path, 4)
		if err := checksum.OpenPath(); err != nil {
			t.Fatal(err)
		}
		for _, flags := range []int{CHECKSUM_MD5, CHECKSUM_SLICE_MD5, CHECKSUM_CRC32, CHECKSUM_MD5 | CHECKSUM_SLICE_MD5 | CHECKSUM_CRC32} {
			if err := checksum.Sum(flags); err != nil {
				t.Fatalf("size=%d flags=%d: %v", size, flags, err)
			}
			full := md5.Sum(data)
			slice := md5.Sum(data[:min(4, size)])
			if flags&CHECKSUM_MD5 != 0 && !bytes.Equal(checksum.MD5, full[:]) {
				t.Errorf("size=%d: full MD5 mismatch", size)
			}
			if flags&CHECKSUM_SLICE_MD5 != 0 && !bytes.Equal(checksum.SliceMD5, slice[:]) {
				t.Errorf("size=%d: slice MD5 mismatch", size)
			}
			if flags&CHECKSUM_CRC32 != 0 && checksum.CRC32 != crc32.ChecksumIEEE(data) {
				t.Errorf("size=%d: CRC32 mismatch", size)
			}
		}
		checksum.Close()
	}
}
