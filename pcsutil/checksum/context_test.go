package checksum

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func cancellationFixture(t *testing.T, size int64) *LocalFileChecksum {
	t.Helper()
	name := filepath.Join(t.TempDir(), "fixture.bin")
	f, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	sum := NewLocalFileChecksum(name, 4)
	if err := sum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sum.Close() })
	sum.MD5, sum.SliceMD5, sum.CRC32 = []byte("previous MD5"), []byte("previous slice"), 42
	sum.BlocksList = []string{"previous block"}
	return sum
}

func TestCanceledChecksumPreservesPreviousSnapshot(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, chunked := range []bool{false, true} {
			size := int64(8)
			if empty {
				size = 0
			}
			sum := cancellationFixture(t, size)
			before := sum.LocalFileMeta
			if _, err := sum.GetFile().Seek(3, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var err error
			if chunked {
				err = sum.CalculateChunkedSumWithContext(ctx, 4)
			} else {
				err = sum.SumWithContext(ctx, CHECKSUM_MD5|CHECKSUM_SLICE_MD5|CHECKSUM_CRC32)
			}
			if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(sum.LocalFileMeta, before) {
				t.Fatalf("canceled checksum changed previous results: empty=%v chunked=%v err=%v", empty, chunked, err)
			}
			if pos, err := sum.GetFile().Seek(0, io.SeekCurrent); err != nil || pos != 3 {
				t.Fatalf("pre-canceled checksum moved the file position: %d err=%v", pos, err)
			}
		}
	}
}

func TestSumCancellationDuringReadPreservesDigestsAndResetsPosition(t *testing.T) {
	sum := cancellationFixture(t, 128<<20)
	before := sum.LocalFileMeta
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	done := make(chan struct{})
	var result error
	go func() { result = sum.SumWithContext(ctx, CHECKSUM_MD5|CHECKSUM_SLICE_MD5|CHECKSUM_CRC32); close(done) }()
	defer func() { cancel(); <-done }()
	for {
		pos, err := sum.GetFile().Seek(0, io.SeekCurrent)
		if err != nil {
			t.Fatal(err)
		}
		if pos >= int64(DefaultBufSize) {
			break
		}
		select {
		case <-done:
			t.Fatalf("checksum finished before cancellation fixture observed reading: %v", result)
		case <-ctx.Done():
			t.Fatal("checksum did not start reading")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("checksum did not stop after cancellation")
	}
	if !errors.Is(result, context.Canceled) || !reflect.DeepEqual(sum.LocalFileMeta, before) {
		t.Fatalf("interrupted checksum published partial results: %v", result)
	}
	if pos, err := sum.GetFile().Seek(0, io.SeekCurrent); err != nil || pos != 0 {
		t.Fatalf("canceled read did not reset position: %d err=%v", pos, err)
	}
}

func TestChunkedSumCancellationPreservesPreviousBlocks(t *testing.T) {
	sum := cancellationFixture(t, 256<<20)
	before := sum.LocalFileMeta
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := time.AfterFunc(time.Millisecond, cancel)
	defer stop.Stop()
	if err := sum.CalculateChunkedSumWithContext(ctx, 4<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("chunked checksum ignored cancellation: %v", err)
	}
	if !reflect.DeepEqual(sum.LocalFileMeta, before) {
		t.Fatal("canceled chunk workers published partial results")
	}
	// The handle remains usable after all readers have been joined.
	buf := make([]byte, 8)
	if n, err := sum.GetFile().ReadAt(buf, 0); err != nil || n != len(buf) || !bytes.Equal(buf, make([]byte, 8)) {
		t.Fatal("canceling checksum invalidated the source handle", err)
	}
}
