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
