package downloader

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestQueuedWriteRechecksSplitBoundary(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	worker := NewWorker(0, "", f)
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	worker.SetDownloadStatus(status)
	worker.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh")))}
	mu := &sync.Mutex{}
	worker.SetWriteMutex(mu)
	mu.Lock()
	release := sync.OnceFunc(mu.Unlock)
	defer release()
	done := make(chan struct{})
	go func() { worker.Execute(); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for worker.status.StatusCode() != StatusCodeWaitToWrite {
		if time.Now().After(deadline) {
			release()
			<-done
			t.Fatal("worker did not queue its write")
		}
		time.Sleep(time.Millisecond)
	}
	// A split can commit while this worker waits for another disk writer.
	// Its buffered data was read for the original, larger interval.
	worker.GetRange().StoreEnd(4)
	release()
	<-done
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "abcd" || worker.GetRange().LoadBegin() != 4 || status.Downloaded() != 4 {
		t.Fatalf("write crossed split boundary: data=%q range=%s downloaded=%d err=%v", got, worker.GetRange().ShowDetails(), status.Downloaded(), err)
	}
}

func TestDynamicSplitUsesCommittedWriteProgress(t *testing.T) {
	total := 16 * MinParallelSize
	mu := &sync.Mutex{}
	worker := NewWorker(0, "", nil)
	worker.SetWriteMutex(mu)
	worker.SetRange(&transfer.Range{Begin: 0, End: total})
	worker.status.SetStatusCode(StatusCodeDownloading)
	idle := NewWorker(1, "", nil)
	idle.SetWriteMutex(mu)
	idle.SetRange(&transfer.Range{Begin: total, End: total})
	idle.status.SetStatusCode(StatusCodeSucceeded)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	idle.parentCtx = ctx
	monitor := NewMonitor()
	monitor.lazyInit()
	monitor.Append(worker)
	monitor.Append(idle)
	mu.Lock()
	release := sync.OnceFunc(mu.Unlock)
	defer release()
	done := make(chan struct{})
	go func() { monitor.DynamicSplitWorker(worker); close(done) }()
	select {
	case <-done:
	case <-time.After(50 * time.Millisecond):
	}
	// Finish the in-flight write before permitting a new split to commit.
	worker.GetRange().AddBegin(3 * total / 4)
	release()
	<-done
	idle.runWG.Wait()
	if worker.GetRange().LoadBegin() != 3*total/4 || worker.GetRange().LoadEnd() != 7*total/8 || idle.GetRange().LoadBegin() != 7*total/8 || idle.GetRange().LoadEnd() != total {
		t.Fatalf("split used stale progress: source=%s spare=%s", worker.GetRange().ShowDetails(), idle.GetRange().ShowDetails())
	}
}
