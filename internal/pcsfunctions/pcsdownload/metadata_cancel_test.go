package pcsdownload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

type metadataCancelTransport func(*http.Request) (*http.Response, error)

func (f metadataCancelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type metadataCancelBody struct {
	ctx              context.Context
	started, release chan struct{}
	closed           bool
}

func TestDownloadTaskMetadataWithoutContextKeepsLegacyBehavior(t *testing.T) {
	target := filepath.Join(t.TempDir(), "empty")
	client := requester.NewHTTPClient()
	client.Client.Transport = metadataCancelTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","isdir":0,"size":0}]}`)), Request: r}, nil
	})
	task := &DownloadTaskUnit{PCS: baidupcs.NewPCSWithClient(1, client), PcsPath: "/fixture", SavePath: target, IsOverwrite: true, Cfg: downloader.NewConfig(), taskInfo: &taskframework.TaskInfo{}}
	result := task.Run()
	info, err := os.Stat(target)
	if !result.Succeed || result.Err != nil || err != nil || info.Size() != 0 {
		t.Fatalf("legacy metadata/download failed: result=%+v info=%v err=%v", result, info, err)
	}
}

func TestDownloadTaskMetadataDeadlineDoesNotRetry(t *testing.T) {
	client := requester.NewHTTPClient()
	client.Client.Transport = metadataCancelTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	task := &DownloadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), PcsPath: "/fixture", taskInfo: &taskframework.TaskInfo{}}
	result := task.Run()
	if result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("expired metadata task retried: %+v", result)
	}
	active := &taskframework.TaskUnitRunResult{Err: result.Err}
	(&DownloadTaskUnit{Context: context.Background()}).handleError(active)
	if !active.NeedRetry {
		t.Fatal("request timeout disabled retries while the parent task was still live")
	}
}

func (b *metadataCancelBody) Read([]byte) (int, error) {
	close(b.started)
	select {
	case <-b.ctx.Done():
		return 0, b.ctx.Err()
	case <-b.release:
		return 0, errors.New("fixture forced release")
	}
}
func (b *metadataCancelBody) Close() error { b.closed = true; return nil }

func TestDownloadTaskMetadataBodyCancellationPreservesOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(target, []byte("keep existing data"), 0600); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	var body *metadataCancelBody
	client := requester.NewHTTPClient()
	client.Client.Transport = metadataCancelTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		body = &metadataCancelBody{r.Context(), started, release, false}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
	})
	task := &DownloadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), PcsPath: "/fixture", SavePath: target, IsOverwrite: true, Cfg: downloader.NewConfig(), taskInfo: &taskframework.TaskInfo{}}
	done, joined := make(chan *taskframework.TaskUnitRunResult, 1), make(chan struct{})
	go func() { defer close(joined); done <- task.Run() }()
	defer func() { cancel(); unblock(); <-joined }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata body was not read")
	}
	cancel()
	select {
	case result := <-done:
		if result.Succeed || result.NeedRetry || !errors.Is(result.Err, context.Canceled) || !body.closed {
			t.Fatalf("metadata cancellation lost: result=%+v closed=%v", result, body.closed)
		}
	case <-time.After(time.Second):
		t.Error("download metadata body ignored task cancellation")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "keep existing data" {
		t.Fatal("metadata cancellation changed output", err)
	}
}
