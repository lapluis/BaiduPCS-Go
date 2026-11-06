package downloader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestExecuteContextCancelsDefaultURLProbes(t *testing.T) {
	for _, phase := range []string{"initial", "load-balancer"} {
		t.Run(phase, func(t *testing.T) {
			started, stopped, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (phase == "initial" && r.URL.Path == "/main") || r.URL.Path == "/secondary" {
					close(started)
					select {
					case <-r.Context().Done():
						close(stopped)
					case <-release:
					}
					return
				}
				w.Header().Set("Content-Length", "0")
			}))
			defer server.Close()
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			client := requester.NewHTTPClient()
			client.SetTimeout(20 * time.Second)
			d := NewDownloader(server.URL+"/main", f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
			d.SetClient(client)
			if phase == "load-balancer" {
				d.AddLoadBalanceServer(server.URL + "/secondary")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- d.ExecuteWithContext(ctx) }()
			joined := false
			defer func() {
				cancel()
				close(release)
				if !joined {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("probe did not exit during cleanup")
					}
				}
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("probe did not start")
			}
			cancel()
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled probe returned %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation waited for the network timeout")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("probe request remained live after Execute returned")
			}
			if client.Timeout != 20*time.Second {
				t.Fatal("probe changed caller timeout")
			}
		})
	}
}
