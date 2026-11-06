package rc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestWorkerDownloadMetadataUsesTaskContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	client := requester.NewHTTPClient()
	client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		close(started)
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return nil, errors.New("fixture forced release")
		}
	})
	done, joined := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(joined)
		_, err := workerDownloadInfo(ctx, baidupcs.NewPCSWithClient(1, client), "/fixture")
		done <- err
	}()
	defer func() { cancel(); unblock(); <-joined }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("metadata request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("metadata cancellation lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("worker metadata ignored task context")
	}
}

func TestWorkerDownloadMetadataPreservesFileChecks(t *testing.T) {
	for _, mode := range []string{"file", "directory", "canceled-reply"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer r.Body.Close()
				}
				isdir := 0
				if mode == "directory" {
					isdir = 1
				}
				if mode == "canceled-reply" {
					cancel()
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &emptyUploadResultBody{Reader: strings.NewReader(fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":1,"path":"/fixture","isdir":%d,"size":0}]}`, isdir))}, Request: r}, nil
			})
			info, err := workerDownloadInfo(ctx, baidupcs.NewPCSWithClient(1, client), "/fixture")
			if mode == "file" {
				if err != nil || info == nil || info.Isdir || info.Size != 0 {
					t.Fatalf("valid file rejected: info=%v err=%v", info, err)
				}
			} else if err == nil || info != nil || mode == "canceled-reply" && !errors.Is(err, context.Canceled) {
				t.Fatalf("invalid/canceled metadata accepted: info=%v err=%v", info, err)
			}
		})
	}
}
