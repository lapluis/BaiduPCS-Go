package rio

import (
	"errors"
	"io"
	"strconv"
	"testing"
)

func TestBufferShortReadAndWrite(t *testing.T) {
	b := NewBuffer([]byte("abcd"))
	out := make([]byte, 3)
	if n, err := b.ReadAt(out, 2); n != 2 || !errors.Is(err, io.EOF) || string(out[:n]) != "cd" {
		t.Fatalf("short read: %d, %v, %q", n, err, out)
	}
	if n, err := b.WriteAt([]byte("XYZ"), 2); n != 2 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write: %d, %v", n, err)
	}
	if string(b.Bytes()) != "abXY" {
		t.Fatal("write corrupted buffer")
	}
	if n, err := b.ReadAt(make([]byte, 4), 0); n != 4 || err != nil {
		t.Fatal("full read failed")
	}
}

func TestBufferInvalidOffsets(t *testing.T) {
	for _, off := range []int64{-1, 4, 5, 1<<63 - 1} {
		t.Run(strconv.FormatInt(off, 10), func(t *testing.T) {
			b := NewBuffer([]byte("abcd"))
			defer func() {
				if recover() != nil {
					t.Fatal("offset caused panic")
				}
			}()
			if n, err := b.ReadAt(make([]byte, 1), off); n != 0 || err == nil {
				t.Error("invalid read accepted")
			}
			if n, err := b.WriteAt([]byte("x"), off); n != 0 || err == nil {
				t.Error("invalid write accepted")
			}
			if string(b.Bytes()) != "abcd" {
				t.Fatal("invalid offset changed buffer")
			}
		})
	}
	b := NewBuffer(nil)
	if n, err := b.ReadAt(nil, 0); n != 0 || err != nil {
		t.Fatal("empty read failed")
	}
	if n, err := b.WriteAt(nil, 0); n != 0 || err != nil {
		t.Fatal("empty write failed")
	}
}
