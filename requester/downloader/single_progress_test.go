package downloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type singleProgressBody struct {
	*bytes.Reader
	reads            int
	blocked, release chan struct{}
	once             sync.Once
}

func (b *singleProgressBody) Read(p []byte) (int, error) {
	b.reads++
	if b.reads == 2 {
		close(b.blocked)
		<-b.release
	}
	return b.Reader.Read(p)
}
func (b *singleProgressBody) Close() error { b.once.Do(func() { close(b.release) }); return nil }

func TestSingleDownloadProgressKeepsKnownTotal(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			// Exceed both configured and default cached buffers so a prior test's
			// pool contents cannot make the first write complete the whole file.
			content := bytes.Repeat([]byte("fixture!"), 1<<17)
			body := &singleProgressBody{Reader: bytes.NewReader(content), blocked: make(chan struct{}), release: make(chan struct{})}
			defer body.Close()
			file, err := os.Create(filepath.Join(t.TempDir(), "data"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			d := NewDownloader("fixture://download/", file, &Config{MaxParallel: 4, CacheSize: 1024})
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, uri string) (int64, *http.Response, error) {
				req, err := http.NewRequest(http.MethodGet, uri, nil)
				if err != nil {
					return 0, nil, err
				}
				size := int64(-1)
				if known {
					size = int64(len(content))
				}
				return size, &http.Response{StatusCode: 200, ContentLength: size, Header: make(http.Header), Body: body, Request: req}, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- d.ExecuteWithContext(ctx) }()
			select {
			case <-body.blocked:
			case <-ctx.Done():
				body.Close()
				<-done
				t.Fatal("stream did not reach second read")
			}
			status := d.monitor.Status()
			total, downloaded := status.TotalSize(), status.Downloaded()
			body.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if downloaded <= 0 || downloaded >= int64(len(content)) {
				t.Fatalf("fixture did not stop mid-download: %d", downloaded)
			}
			wantTotal := downloaded
			if known {
				wantTotal = int64(len(content))
			}
			if total != wantTotal {
				t.Fatalf("single download total=%d want=%d downloaded=%d", total, wantTotal, downloaded)
			}
			if status.TotalSize() != int64(len(content)) || status.Downloaded() != int64(len(content)) {
				t.Fatal("final download statistics changed")
			}
			got, err := os.ReadFile(file.Name())
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("downloaded content changed: bytes=%d err=%v", len(got), err)
			}
		})
	}
}

func TestStandaloneSingleWorkerAccumulatesUnknownSize(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	status := transfer.NewDownloadStatus()
	worker := NewWorker(0, "", file)
	worker.SetDownloadStatus(status)
	worker.firstResp = &http.Response{StatusCode: 200, ContentLength: -1, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte("fixture")))}
	worker.Execute()
	if worker.Err() != nil || worker.GetStatus().StatusCode() != StatusCodeSucceeded || status.TotalSize() != 7 || status.Downloaded() != 7 {
		t.Fatalf("standalone unknown-length progress changed: err=%v total=%d downloaded=%d", worker.Err(), status.TotalSize(), status.Downloaded())
	}
}
