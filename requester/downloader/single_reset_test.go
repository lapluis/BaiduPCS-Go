package downloader

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type partialSingleBody struct {
	first           bool
	blocked, closed chan struct{}
	once            sync.Once
}

func (b *partialSingleBody) Read(p []byte) (int, error) {
	if !b.first {
		b.first = true
		return copy(p, []byte("abcd")), nil
	}
	close(b.blocked)
	<-b.closed
	return 0, io.ErrClosedPipe
}
func (b *partialSingleBody) Close() error { b.once.Do(func() { close(b.closed) }); return nil }

func TestSingleStreamResetCannotAppendFullReplay(t *testing.T) {
	for _, size := range []int64{8, -1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			body := &partialSingleBody{blocked: make(chan struct{}), closed: make(chan struct{})}
			defer body.Close()
			w := NewWorker(0, "http://download.fixture.invalid/file", f)
			w.cacheSize = 4
			w.SetTotalSize(size)
			w.SetRange(&transfer.Range{Begin: 0, End: -2})
			status := transfer.NewDownloadStatus()
			status.SetTotalSize(max(size, 0))
			w.SetDownloadStatus(status)
			w.firstResp = &http.Response{StatusCode: 200, ContentLength: size, Header: make(http.Header), Body: body}
			calls := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, ContentLength: size, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh"))), Request: r}, nil
			})
			w.SetClient(client)
			done := make(chan struct{})
			go func() { w.Execute(); close(done) }()
			select {
			case <-body.blocked:
			case <-time.After(time.Second):
				body.Close()
				<-done
				t.Fatal("partial write did not finish")
			}
			w.Reset()
			joined := make(chan struct{})
			go func() { w.runWG.Wait(); <-done; close(joined) }()
			select {
			case <-joined:
			case <-time.After(time.Second):
				w.Cancel()
				t.Fatal("reset did not finish")
			}
			data, err := os.ReadFile(f.Name())
			if err != nil || string(data) != "abcd" || status.Downloaded() != 4 || calls != 0 || w.GetStatus().StatusCode() != StatusCodeInternalError || w.Err() == nil || !strings.Contains(w.Err().Error(), "single stream") {
				t.Fatalf("full stream replay appended to partial data: size=%d data=%q downloaded=%d calls=%d status=%v err=%v read=%v", size, data, status.Downloaded(), calls, w.GetStatus().StatusCode(), w.Err(), err)
			}
		})
	}
}

func TestCompletedWorkerResetDoesNotRestart(t *testing.T) {
	for _, state := range []StatusCode{StatusCodeSucceeded, StatusCodeCanceled} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("abcdefgh"); err != nil {
				t.Fatal(err)
			}
			w := NewWorker(0, "http://download.fixture.invalid/file", f)
			w.SetRange(&transfer.Range{Begin: 8, End: 8})
			w.SetTotalSize(8)
			w.status.SetStatusCode(state)
			var cause error
			if state == StatusCodeCanceled {
				cause = errors.New("original cancellation")
				w.err = cause
			}
			resets, calls := 0, 0
			w.resetFunc = func() { resets++ }
			client := requester.NewHTTPClient()
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, ContentLength: 8, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader([]byte("abcdefgh"))), Request: r}, nil
			})
			w.SetClient(client)
			w.Reset()
			w.runWG.Wait()
			data, err := os.ReadFile(f.Name())
			if err != nil || string(data) != "abcdefgh" || resets != 0 || calls != 0 || w.GetStatus().StatusCode() != state || w.Err() != cause {
				t.Fatal("terminal reset restarted worker", string(data), resets, calls, w.GetStatus().StatusCode(), w.Err(), err)
			}
		})
	}
}

type unusedSingleResponse struct {
	io.Reader
	reads  int
	closed bool
}

func (b *unusedSingleResponse) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *unusedSingleResponse) Close() error               { b.closed = true; return nil }

func TestSingleNonzeroStartClosesUnusedResponse(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("abcd"); err != nil {
		t.Fatal(err)
	}
	body := &unusedSingleResponse{Reader: bytes.NewReader([]byte("abcdefgh"))}
	w := NewWorker(0, "", f)
	w.SetRange(&transfer.Range{Begin: 4, End: -2})
	w.SetTotalSize(8)
	w.firstResp = &http.Response{StatusCode: 200, ContentLength: 8, Header: make(http.Header), Body: body}
	w.Execute()
	data, err := os.ReadFile(f.Name())
	if err != nil || string(data) != "abcd" || !body.closed || body.reads != 0 || w.firstResp != nil || w.GetStatus().StatusCode() != StatusCodeInternalError {
		t.Fatal("invalid start consumed response or changed prefix", string(data), body.closed, body.reads, w.Err(), err)
	}
}

func TestRangedWorkerCanContinueNonzeroOffset(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("abcd"); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(0, "http://download.fixture.invalid/file", f)
	w.SetAcceptRange("bytes")
	w.SetRange(&transfer.Range{Begin: 4, End: 8})
	w.SetTotalSize(8)
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	status.AddDownloaded(4)
	w.SetDownloadStatus(status)
	calls := 0
	client := requester.NewHTTPClient()
	client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Range") != "bytes=4-7" {
			t.Error("wrong remaining range", r.Header.Get("Range"))
		}
		return &http.Response{StatusCode: 206, ContentLength: 4, Header: http.Header{"Content-Range": []string{"bytes 4-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("efgh"))), Request: r}, nil
	})
	w.SetClient(client)
	w.Execute()
	data, err := os.ReadFile(f.Name())
	if err != nil || string(data) != "abcdefgh" || calls != 1 || status.Downloaded() != 8 || w.GetStatus().StatusCode() != StatusCodeSucceeded || w.Err() != nil {
		t.Fatal("valid range resume rejected", string(data), calls, status.Downloaded(), w.Err(), err)
	}
}
