package rc

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestQueuedRemoteOperationCancellation(t *testing.T) {
	for _, operation := range []string{"stat", "list", "mkdir", "remove", "copy", "move"} {
		t.Run(operation, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			var calls atomic.Int32
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer r.Body.Close()
				}
				n := calls.Add(1)
				if n > 2 {
					return nil, errors.New("queued canceled operation issued a request")
				}
				payload := `{"error_code":0,"list":[{"path":"/followup","fs_id":2,"size":3}]}`
				if n == 1 {
					close(started)
					<-release
					payload = `{"error_code":0,"list":[{"path":"/held","fs_id":1,"size":3}]}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &emptyUploadResultBody{Reader: strings.NewReader(payload)}, Request: r}, nil
			})
			remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
			owner := make(chan struct{})
			var ownerErr error
			go func() { defer close(owner); _, ownerErr = remote.Stat(context.Background(), "/held") }()
			defer func() {
				unblock()
				<-owner
				if ownerErr != nil {
					t.Error("owner affected by queued cancellation", ownerErr)
				}
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("owner did not acquire remote")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "stat":
					_, err = remote.Stat(ctx, "/queued")
				case "list":
					_, err = remote.List(ctx, "/queued")
				case "mkdir":
					err = remote.Mkdir(ctx, "/queued")
				case "remove":
					err = remote.Remove(ctx, "/queued", false)
				case "copy":
					err = remote.copyFile(ctx, "/queued", "/target")
				case "move":
					err = remote.moveFile(ctx, "/queued", "/target")
				}
				done <- err
			}()
			select {
			case err := <-done:
				t.Fatal("operation escaped held client lock", err)
			case <-time.After(20 * time.Millisecond):
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				unblock()
				<-done
				t.Fatal("queued cancellation waited for unrelated request")
			}
			if calls.Load() != 1 {
				t.Fatalf("unexpected calls %d", calls.Load())
			}
			unblock()
			<-owner
			followCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			entry, err := remote.Stat(followCtx, "/followup")
			if err != nil || entry.Size != 3 || calls.Load() != 2 {
				t.Fatal("client unusable after queued cancellation", entry, err)
			}
		})
	}
}
