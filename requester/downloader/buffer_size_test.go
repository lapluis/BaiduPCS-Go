package downloader

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type bufferSizeBody struct {
	*bytes.Reader
	firstBuffer int
}

func (b *bufferSizeBody) Read(p []byte) (int, error) {
	if b.firstBuffer == 0 {
		b.firstBuffer = len(p)
	}
	return b.Reader.Read(p)
}
func (*bufferSizeBody) Close() error { return nil }

func TestConcurrentDownloadsKeepTheirOwnBufferSizes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	makeDownload := func(name string, size int) (*Downloader, *bufferSizeBody) {
		file, err := os.Create(filepath.Join(t.TempDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { file.Close() })
		body := &bufferSizeBody{Reader: bytes.NewReader([]byte("fixture"))}
		d := NewDownloader("fixture://"+name, file, &Config{MaxParallel: 1, CacheSize: size})
		d.SetDURLCheckFunc(func(_ *requester.HTTPClient, uri string) (int64, *http.Response, error) {
			req, err := http.NewRequest(http.MethodGet, uri, nil)
			return 7, &http.Response{StatusCode: 200, ContentLength: 7, Header: make(http.Header), Body: body, Request: req}, err
		})
		return d, body
	}
	small, smallBody := makeDownload("small", 1024)
	large, largeBody := makeDownload("large", 8192)
	smallReady, largeReady := make(chan struct{}), make(chan struct{})
	smallRelease, largeRelease := make(chan struct{}), make(chan struct{})
	releaseSmall := sync.OnceFunc(func() { close(smallRelease) })
	releaseLarge := sync.OnceFunc(func() { close(largeRelease) })
	small.OnExecute(func() {
		close(smallReady)
		select {
		case <-smallRelease:
		case <-ctx.Done():
		}
	})
	large.OnExecute(func() {
		close(largeReady)
		select {
		case <-largeRelease:
		case <-ctx.Done():
		}
	})
	smallDone, largeDone := make(chan error, 1), make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(2)
	defer func() { releaseSmall(); releaseLarge(); cancel(); wg.Wait() }()
	go func() { defer wg.Done(); smallDone <- small.ExecuteWithContext(ctx) }()
	go func() {
		defer wg.Done()
		select {
		case <-smallReady:
		case <-ctx.Done():
			largeDone <- ctx.Err()
			return
		}
		largeDone <- large.ExecuteWithContext(ctx)
	}()
	select {
	case <-largeReady:
	case <-ctx.Done():
		t.Fatal("downloads did not reach prepared state")
	}
	// Both are configured, with the large download configured last. Starting
	// the small download first exposes accidental use of a shared size.
	releaseSmall()
	if err := <-smallDone; err != nil {
		t.Fatal(err)
	}
	releaseLarge()
	if err := <-largeDone; err != nil {
		t.Fatal(err)
	}
	if smallBody.firstBuffer != 1024 || largeBody.firstBuffer != 8192 {
		t.Fatalf("download buffer configuration leaked: small=%d large=%d", smallBody.firstBuffer, largeBody.firstBuffer)
	}
}
