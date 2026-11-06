package pcsupload

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
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/checksum"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestNoRapidPreparationCancellationReachesHeadersAndBody(t *testing.T) {
	for _, phase := range []string{"quota", "meta", "precreate"} {
		for _, bodyBlocked := range []bool{false, true} {
			t.Run(phase+map[bool]string{false: "/headers", true: "/body"}[bodyBlocked], func(t *testing.T) {
				root := t.TempDir()
				source := filepath.Join(root, "file")
				if err := os.WriteFile(source, []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
				if phase == "quota" {
					if err := os.Truncate(source, baidupcs.MinCheckLeftSpaceThreshold+1); err != nil {
						t.Fatal(err)
					}
				}
				local := checksum.NewLocalFileChecksum(source, 4)
				if err := local.OpenPath(); err != nil {
					t.Fatal(err)
				}
				defer local.Close()
				db, err := NewUploadingDatabaseAt(filepath.Join(root, "resume.json"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				started, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var once sync.Once
				client := requester.NewHTTPClient()
				client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
					isMeta := r.URL.Query().Get("method") == "meta"
					current := "precreate"
					if isMeta {
						current = "meta"
					} else if strings.HasSuffix(r.URL.Path, "/quota") {
						current = "quota"
					}
					if phase == current {
						if bodyBlocked {
							return &http.Response{StatusCode: 200, Header: http.Header{}, Request: r, Body: &lifecycleResponseBody{ctx: r.Context(), release: release, started: started, closed: closed}}, nil
						}
						once.Do(func() { close(started) })
						select {
						case <-r.Context().Done():
							return nil, r.Context().Err()
						case <-release:
							return nil, errors.New("fixture released")
						}
					}
					data := `{"error_code":31066}`
					if !isMeta {
						data = `{"errno":0,"return_type":1,"uploadid":"fixture"}`
					}
					if current == "quota" {
						data = `{"quota":1099511627776,"used":0}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
				})
				ctx, cancel := context.WithCancel(context.Background())
				task := &UploadTaskUnit{Context: ctx, PCS: baidupcs.NewPCSWithClient(1, client), LocalFileChecksum: local, UploadingDatabase: db, SavePath: "/fixture", NoRapidUpload: true, Policy: baidupcs.OverWritePolicy, taskInfo: &taskframework.TaskInfo{}}
				done := make(chan struct{})
				go func() { task.prepareFile(); close(done) }()
				defer func() { cancel(); close(release); <-done }()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("preparation request did not start")
				}
				cancel()
				select {
				case <-done:
					if task.Step != JustGoon || task.prepareResult == nil || !errors.Is(task.prepareResult.Err, context.Canceled) {
						t.Fatalf("cancellation fell through to another upload phase: step=%v result=%+v", task.Step, task.prepareResult)
					}
				case <-time.After(time.Second):
					t.Fatal("preparation request ignored task cancellation")
				}
				if bodyBlocked {
					select {
					case <-closed:
					default:
						t.Error("canceled response body was not closed")
					}
				}
			})
		}
	}
}

func TestCanceledNoRapidHandshakeDoesNotSendRequests(t *testing.T) {
	client := requester.NewHTTPClient()
	requests := 0
	client.Client.Transport = uploadRoundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("unexpected request after cancellation")
	})
	pcs := baidupcs.NewPCSWithClient(1, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, target := range []string{"/fixture", "/"} {
		err, _ := pcs.FakeRapidUploadWithContext(ctx, target, baidupcs.OverWritePolicy, 8)
		if err == nil || !errors.Is(err.GetError(), context.Canceled) || requests != 0 {
			t.Fatalf("pre-canceled handshake: target=%q requests=%d err=%v", target, requests, err)
		}
	}
}
