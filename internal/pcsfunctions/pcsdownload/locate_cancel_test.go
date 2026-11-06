package pcsdownload

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestDownloadTaskLocateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	client := requester.NewHTTPClient()
	client.Client.Transport = metadataCancelTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return nil, errors.New("fixture forced release")
		}
	})
	task := &DownloadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), PcsPath: "/fixture"}
	task.PCS.SetUID(1)
	done, joined := make(chan *taskframework.TaskUnitRunResult, 1), make(chan struct{})
	go func() {
		defer close(joined)
		result := &taskframework.TaskUnitRunResult{}
		task.locateDownload(result)
		done <- result
	}()
	defer func() { cancel(); unblock(); <-joined }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("link lookup did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("link cancellation lost: %+v", result)
		}
	case <-time.After(time.Second):
		t.Error("link lookup ignored task cancellation")
	}
}
