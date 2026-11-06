package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManualCancelStopsInitialDefaultProbe(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		for _, secondary := range []bool{false, true} {
			t.Run(fmt.Sprintf("context=%v/secondary=%v", contextual, secondary), func(t *testing.T) {
				started, stopped := make(chan struct{}), make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if secondary && r.URL.Path == "/main" {
						w.Header().Set("Content-Length", "0")
						return
					}
					close(started)
					<-r.Context().Done()
					close(stopped)
				}))
				defer server.Close()
				f, err := os.Create(filepath.Join(t.TempDir(), "data"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				d := NewDownloader(server.URL+"/main", f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
				if contextual {
					d.SetDURLCheckFuncWithContext(defaultDURLCheckWithContext)
				}
				if secondary {
					d.AddLoadBalanceServer(server.URL + "/secondary")
				}
				ctx, stop := context.WithCancel(context.Background())
				defer stop()
				done := make(chan error, 1)
				go func() { done <- d.ExecuteWithContext(ctx) }()
				joined := false
				defer func() {
					stop()
					if !joined {
						select {
						case <-done:
						case <-time.After(3 * time.Second):
							t.Error("download did not exit during cleanup")
						}
					}
				}()
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("probe did not start")
				}
				d.Cancel()
				select {
				case err := <-done:
					joined = true
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("Cancel ignored the initial probe")
				}
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("probe request remained live")
				}
				if ctx.Err() != nil {
					t.Fatal("Cancel propagated into the caller's parent context")
				}
			})
		}
	}
}

func TestCancelInStartCallbackPreventsWorkerRequests(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", 403)
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
	d.SetFileContentLength(8)
	d.OnExecute(d.Cancel)
	var success atomic.Bool
	d.OnSuccess(func() { success.Store(true) })
	if err := d.Execute(); !errors.Is(err, context.Canceled) || success.Load() || requests.Load() != 0 {
		t.Fatalf("start cancellation did not stop workers: err=%v success=%v requests=%d", err, success.Load(), requests.Load())
	}
}

func TestDownloadStartCallbackPrecedesTransferAndFinalEvents(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader("", f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
	d.SetFileContentLength(0)
	started, released, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(released) })
	defer release()
	d.OnExecute(func() { close(started); <-released; close(exited) })
	var wrongOrder atomic.Bool
	checkStart := func() {
		select {
		case <-exited:
		default:
			wrongOrder.Store(true)
		}
	}
	d.OnSuccess(checkStart)
	d.OnFinish(checkStart)
	done := make(chan error, 1)
	go func() { done <- d.Execute() }()
	joined := false
	defer func() {
		release()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("startup did not exit")
			}
		}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("start callback not invoked")
	}
	early := false
	var runErr error
	select {
	case runErr = <-done:
		early = true
		joined = true
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if !joined {
		select {
		case runErr = <-done:
			joined = true
		case <-time.After(2 * time.Second):
			t.Fatal("startup did not complete")
		}
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("start callback left running")
	}
	if runErr != nil || early || wrongOrder.Load() {
		t.Fatalf("start callback overtaken: err=%v early=%v wrong-order=%v", runErr, early, wrongOrder.Load())
	}
}
