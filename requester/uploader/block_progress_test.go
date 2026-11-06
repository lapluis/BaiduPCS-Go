package uploader

import (
	"io"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestConcurrentBlockProgressIsAtomic(t *testing.T) {
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	const a, b, end = int64(1<<32 - 1), int64(1 << 32), int64(1 << 33)
	block := &fileBlock{readRange: transfer.Range{Begin: 0, End: end}}
	if _, err := block.Seek(a, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var ready, finished sync.WaitGroup
	var stop, invalid atomic.Bool
	var reads, bad atomic.Int64
	start := make(chan struct{})
	ready.Add(4)
	finished.Add(4)
	for i := 0; i < 3; i++ {
		go func() {
			defer finished.Done()
			ready.Done()
			<-start
			for !stop.Load() {
				value := block.Readed()
				left := block.Left()
				reads.Add(1)
				if value != a && value != b || left != end-a && left != end-b {
					bad.Store(value)
					invalid.Store(true)
					stop.Store(true)
				}
			}
		}()
	}
	go func() {
		defer finished.Done()
		defer stop.Store(true)
		ready.Done()
		<-start
		for i := 0; i < 200000 && !stop.Load(); i++ {
			block.Seek(a, io.SeekStart)
			block.Seek(b, io.SeekStart)
		}
	}()
	ready.Wait()
	close(start)
	finished.Wait()
	if invalid.Load() || reads.Load() == 0 {
		t.Fatalf("concurrent progress tore a 64-bit value: read=%d observations=%d", bad.Load(), reads.Load())
	}
}

type blockedProgressReader struct {
	started, release chan struct{}
}

func (r *blockedProgressReader) ReadAt(p []byte, _ int64) (int, error) {
	close(r.started)
	<-r.release
	return copy(p, "abcdefgh"), nil
}

func TestBlockProgressRemainsAvailableDuringBlockedRead(t *testing.T) {
	reader := &blockedProgressReader{started: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(reader.release) })
	block := &fileBlock{readRange: transfer.Range{Begin: 0, End: 8}, readerAt: reader}
	readDone := make(chan struct{})
	var n int
	var readErr error
	go func() { n, readErr = block.Read(make([]byte, 8)); close(readDone) }()
	defer func() { release(); <-readDone }()
	<-reader.started
	statsDone := make(chan struct{})
	var readed, left int64
	go func() { readed, left = block.Readed(), block.Left(); close(statsDone) }()
	select {
	case <-statsDone:
		if readed != 0 || left != 8 {
			t.Fatalf("uncommitted read changed progress: readed=%d left=%d", readed, left)
		}
	case <-time.After(time.Second):
		release()
		<-readDone
		<-statsDone
		t.Fatal("progress query waited for blocked file I/O")
	}
	release()
	<-readDone
	if n != 8 || readErr != nil || block.Readed() != 8 || block.Left() != 0 {
		t.Fatalf("completed read progress incorrect: n=%d err=%v readed=%d left=%d", n, readErr, block.Readed(), block.Left())
	}
}
