package checksum_test

import (
	"bytes"
	"crypto/md5"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func TestChecksum(t *testing.T) {
	data := bytes.Repeat([]byte("checksum fixture"), 20000)
	p := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	flags := checksum.CHECKSUM_MD5 | checksum.CHECKSUM_SLICE_MD5 | checksum.CHECKSUM_CRC32
	got, err := checksum.GetFileSum(p, flags)
	if err != nil {
		t.Fatal(err)
	}
	want := md5.Sum(data)
	slice := md5.Sum(data[:256*1024])
	if !bytes.Equal(got.MD5, want[:]) || !bytes.Equal(got.SliceMD5, slice[:]) || got.CRC32 != crc32.ChecksumIEEE(data) {
		t.Fatal("checksum mismatch")
	}
	lf := checksum.NewLocalFileChecksumWithBufSize(p, checksum.DefaultBufSize-3, checksum.DefaultBufSize)
	if err = lf.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	if err = lf.Sum(flags); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lf.MD5, want[:]) {
		t.Fatal("unaligned buffer hash mismatch")
	}
}
