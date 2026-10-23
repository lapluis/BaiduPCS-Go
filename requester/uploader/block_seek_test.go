package uploader

import (
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"io"
	"strings"
	"testing"
)

func TestBufferedSplitSeekDiscardsOldData(t *testing.T) {
	reader := NewBufioSplitUnit(strings.NewReader("abcdefgh"), transfer.Range{Begin: 1, End: 7}, nil, nil)
	first := make([]byte, 1)
	if _, err := io.ReadFull(reader, first); err != nil || string(first) != "b" {
		t.Fatal("initial read failed", err)
	}
	if position, err := reader.Seek(0, io.SeekCurrent); err != nil || position != 1 {
		t.Fatalf("current position includes prefetched data: %d, %v", position, err)
	}
	if _, err := io.ReadFull(reader, first); err != nil || string(first) != "c" {
		t.Fatal("current seek read wrong data", err)
	}
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "bcdefg" {
		t.Fatalf("seek retained stale buffered bytes: %q, %v", data, err)
	}
	if _, err := reader.Seek(-2, io.SeekEnd); err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(reader)
	if err != nil || string(data) != "fg" {
		t.Fatalf("seek from end failed: %q, %v", data, err)
	}
}

func TestLargeSplitRangeDoesNotNarrowRemainingLength(t *testing.T) {
	block := &fileBlock{readerAt: strings.NewReader("abc"), readRange: transfer.Range{Begin: 0, End: 1<<31 + 8}}
	buffer := make([]byte, 3)
	n, err := block.Read(buffer)
	if err != nil || n != 3 || string(buffer) != "abc" {
		t.Fatalf("large range incorrectly exhausted: %d, %v, %q", n, err, buffer)
	}
	if block.Left() != (1<<31+8)-3 {
		t.Fatal("remaining byte count changed incorrectly")
	}
}
