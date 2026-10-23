package checksum

import (
	"bytes"
	"crypto/md5"
	"testing"
)

func TestEqualLengthMD5RequiresCompleteDigests(t *testing.T) {
	for _, digest := range [][]byte{nil, {}, {1}, bytes.Repeat([]byte{1}, md5.Size-1), bytes.Repeat([]byte{1}, md5.Size+1)} {
		a := &LocalFileMeta{Length: 7, MD5: digest}
		b := &LocalFileMeta{Length: 7, MD5: digest}
		if a.EqualLengthMD5(b) {
			t.Errorf("accepted %d-byte MD5 as file identity", len(digest))
		}
	}
	hash := md5.Sum([]byte("fixture"))
	a := &LocalFileMeta{Length: 7, MD5: hash[:]}
	b := &LocalFileMeta{Length: 7, MD5: append([]byte{}, hash[:]...)}
	if !a.EqualLengthMD5(b) {
		t.Fatal("equal complete hashes did not match")
	}
	b.Length++
	if a.EqualLengthMD5(b) {
		t.Fatal("different lengths matched")
	}
	b.Length = a.Length
	b.MD5[0]++
	if a.EqualLengthMD5(b) {
		t.Fatal("different hashes matched")
	}
	if a.EqualLengthMD5(nil) || (*LocalFileMeta)(nil).EqualLengthMD5(a) {
		t.Fatal("nil metadata matched")
	}
}
