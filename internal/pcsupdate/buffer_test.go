package pcsupdate

import "testing"

func TestUpdateBufferRejectsInvalidMetadata(t *testing.T) {
	for _, size := range []int64{-1, -1 << 63, 0, 1 << 31, 1<<63 - 1} {
		buffer, err := newUpdateBuffer(size)
		if err == nil || buffer != nil {
			t.Errorf("invalid archive size %d accepted", size)
		}
	}
	buffer, err := newUpdateBuffer(16)
	if err != nil || len(buffer.Bytes()) != 16 {
		t.Fatal("valid archive allocation failed", err)
	}
}
