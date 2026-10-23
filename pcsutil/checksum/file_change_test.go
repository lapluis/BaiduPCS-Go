package checksum

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSumRejectsFileGrowth(t *testing.T) {
	for _, size := range []int{0, 2, DefaultBufSize} {
		for _, flags := range []int{CHECKSUM_MD5, CHECKSUM_SLICE_MD5, CHECKSUM_CRC32, CHECKSUM_MD5 | CHECKSUM_SLICE_MD5 | CHECKSUM_CRC32} {
			path := filepath.Join(t.TempDir(), "fixture.bin")
			if err := os.WriteFile(path, bytes.Repeat([]byte("a"), size), 0600); err != nil {
				t.Fatal(err)
			}
			checksum := NewLocalFileChecksum(path, 2)
			if err := checksum.OpenPath(); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				checksum.Close()
				t.Fatal(err)
			}
			_, err = file.Write([]byte("extra"))
			file.Close()
			if err != nil {
				checksum.Close()
				t.Fatal(err)
			}
			if err := checksum.Sum(flags); err == nil {
				t.Errorf("size=%d flags=%d: grown file reported success", size, flags)
			}
			if checksum.MD5 != nil || checksum.SliceMD5 != nil || checksum.CRC32 != 0 {
				t.Error("changed file published digests")
			}
			checksum.Close()
		}
	}
}

func TestSumRejectsSameSizeSubsecondChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	stamp := time.Unix(1700000000, 100000000)
	if err := os.WriteFile(path, []byte("aaaa"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 2)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	if err := os.WriteFile(path, []byte("bbbb"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp = stamp.Add(700 * time.Millisecond)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := checksum.Sum(CHECKSUM_MD5); err == nil {
		t.Fatal("same-size modified file reported success with old snapshot metadata")
	}
}
