package checksum_test

import (
	"bytes"
	"crypto/md5"
	"errors"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
)

func FuzzChecksumWriteBoundaries(f *testing.F) {
	for _, data := range [][]byte{nil, []byte("a"), []byte("abcdefgh"), bytes.Repeat([]byte("digest"), 40)} {
		for _, bounds := range [][2]uint16{{0, 0}, {1, 0}, {8, 3}, {8, 7}, {8, 8}, {65535, 65535}} {
			for _, onlySlice := range []bool{false, true} {
				f.Add(data, []byte{0, 1, 7, 63}, bounds[0], bounds[1], onlySlice)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data, chunks []byte, endValue, sliceValue uint16, onlySlice bool) {
		if len(data) > 4096 || len(chunks) > 64 {
			t.Skip()
		}
		end := int(endValue) % (len(data) + 1)
		sliceEnd := 1 + int(sliceValue)%(len(data)+2)
		prefixEnd := min(end, sliceEnd)
		unit := &checksum.ChecksumWriteUnit{
			End: int64(end), SliceEnd: int64(sliceEnd), OnlySliceSum: onlySlice,
			ChecksumWriter: checksum.NewHashChecksumWriter(md5.New()),
		}
		consumed, step, pos := 0, 0, 0
		stopped := false
		for pos < len(data) && !stopped {
			size := 1
			if len(chunks) != 0 {
				size += int(chunks[step%len(chunks)])
			}
			next := min(len(data), pos+size)
			n, err := unit.Write(data[pos:next])
			if n < 0 || n > next-pos || (err != nil && !errors.Is(err, checksum.ErrChecksumWriteStop)) {
				t.Fatalf("invalid write result: n=%d size=%d err=%v", n, next-pos, err)
			}
			consumed += n
			stopped = errors.Is(err, checksum.ErrChecksumWriteStop)
			pos, step = next, step+1
		}
		if !stopped {
			// Exact boundaries publish completion on the next empty write.
			n, err := unit.Write(nil)
			if n != 0 || !errors.Is(err, checksum.ErrChecksumWriteStop) {
				t.Fatalf("digest did not finish: n=%d err=%v", n, err)
			}
		}
		wantConsumed := end
		if onlySlice {
			wantConsumed = prefixEnd
		}
		if consumed != wantConsumed {
			t.Fatalf("hashed %d bytes, want %d", consumed, wantConsumed)
		}
		wantPrefix := md5.Sum(data[:prefixEnd])
		prefix, ok := unit.SliceSum.([]byte)
		if !ok || !bytes.Equal(prefix, wantPrefix[:]) {
			t.Fatalf("prefix digest mismatch: end=%d slice=%d consumed=%d", end, sliceEnd, consumed)
		}
		if onlySlice {
			if unit.Sum != nil {
				t.Fatal("slice-only operation published a whole-file digest")
			}
		} else {
			want := md5.Sum(data[:end])
			whole, ok := unit.Sum.([]byte)
			if !ok || !bytes.Equal(whole, want[:]) {
				t.Fatal("whole-file digest mismatch")
			}
		}
		beforePrefix, beforeSum := unit.SliceSum, unit.Sum
		if n, err := unit.Write([]byte("extra")); n != 0 || !errors.Is(err, checksum.ErrChecksumWriteStop) {
			t.Fatalf("completed digest consumed more bytes: n=%d err=%v", n, err)
		}
		if !bytes.Equal(unit.SliceSum.([]byte), beforePrefix.([]byte)) {
			t.Fatal("completed prefix digest changed")
		}
		if beforeSum != nil && !bytes.Equal(unit.Sum.([]byte), beforeSum.([]byte)) {
			t.Fatal("completed whole-file digest changed")
		}
	})
}
