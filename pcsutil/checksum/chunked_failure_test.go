package checksum

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestChunkedSumRejectsTruncatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.bin")
	data := []byte("fixture")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	checksum.BlocksList = []string{"previous complete snapshot"}
	if err := os.Truncate(path, 2); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- checksum.CalculateChunkedSum(4) }()
	select {
	case err := <-done:
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("got %v", err)
		}
		if !reflect.DeepEqual(checksum.BlocksList, []string{"previous complete snapshot"}) {
			t.Fatal("failure published partial block hashes")
		}
	case <-time.After(time.Second):
		// Repair this owned fixture so the old infinite loop can exit cleanly.
		if err := os.WriteFile(path, data, 0600); err != nil {
			checksum.Close()
		}
		<-done
		t.Fatal("chunked checksum did not return after premature EOF")
	}
}

func TestChunkedSumBoundaries(t *testing.T) {
	data := []byte("fixture")
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	for _, chunkSize := range []int64{1, 3, 7, 10, 1<<63 - 1} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("chunkSize=%d panicked: %v", chunkSize, r)
				}
			}()
			if err := checksum.CalculateChunkedSum(chunkSize); err != nil {
				t.Errorf("chunkSize=%d: %v", chunkSize, err)
				return
			}
			var want []string
			for offset := int64(0); offset < int64(len(data)); {
				end := offset + min(chunkSize, int64(len(data))-offset)
				hash := md5.Sum(data[offset:end])
				want = append(want, hex.EncodeToString(hash[:]))
				offset = end
			}
			if !reflect.DeepEqual(checksum.BlocksList, want) {
				t.Errorf("chunkSize=%d: got %v want %v", chunkSize, checksum.BlocksList, want)
			}
		}()
	}
}

func TestChunkedSumEmptyClearsPreviousBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.bin")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	checksum.BlocksList = []string{"previous"}
	if err := checksum.CalculateChunkedSum(4); err != nil {
		t.Fatal(err)
	}
	if len(checksum.BlocksList) != 0 {
		t.Fatal("empty file retained old block hashes")
	}
}

func TestChunkedSumReadFailureKeepsPreviousSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "write-only.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("fixture")); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	checksum.file, checksum.Length = file, 7
	checksum.BlocksList = []string{"previous complete snapshot"}
	if err := checksum.CalculateChunkedSum(3); err == nil {
		t.Fatal("failed ReadAt reported success")
	}
	if !reflect.DeepEqual(checksum.BlocksList, []string{"previous complete snapshot"}) {
		t.Fatal("read failure published partial hashes")
	}
}

func TestChunkedSumAcrossReadBufferBoundary(t *testing.T) {
	data := make([]byte, 4*1024*1024+15)
	for i := range data {
		data[i] = byte(i % 251)
	}
	path := filepath.Join(t.TempDir(), "fixture.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	checksum := NewLocalFileChecksum(path, 4)
	if err := checksum.OpenPath(); err != nil {
		t.Fatal(err)
	}
	defer checksum.Close()
	const chunkSize = 4*1024*1024 + 7
	if err := checksum.CalculateChunkedSum(chunkSize); err != nil {
		t.Fatal(err)
	}
	first, second := md5.Sum(data[:chunkSize]), md5.Sum(data[chunkSize:])
	want := []string{hex.EncodeToString(first[:]), hex.EncodeToString(second[:])}
	if !reflect.DeepEqual(checksum.BlocksList, want) {
		t.Fatalf("got %v want %v", checksum.BlocksList, want)
	}
}
