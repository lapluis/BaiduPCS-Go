package pcsupload

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/uploader"
)

type uploadRoundTrip func(*http.Request) (*http.Response, error)

func (f uploadRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type uploadTestReader struct{ *bytes.Reader }

func (r uploadTestReader) Len() int64 { return r.Size() }

func testPCSUploadClient(t *testing.T, transport uploadRoundTrip) *PCSUpload {
	t.Helper()
	uploadClientOnce.Do(func() { uploadClient = requester.NewHTTPClient() })
	previous := uploadClient
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = transport
	uploadClient = client
	t.Cleanup(func() { uploadClient = previous })
	return &PCSUpload{pcs: baidupcs.NewPCSWithClient(1, client), targetPath: "/synthetic"}
}

func TestPCSUploadCancellationReachesHTTPRequest(t *testing.T) {
	started, stopped, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer close(release)
	pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
		close(started)
		select {
		case <-r.Context().Done():
			close(stopped)
			return nil, r.Context().Err()
		case <-release:
			return nil, errors.New("released fixture")
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := pu.TmpFile(ctx, "synthetic", "/synthetic", 0, 0, uploadTestReader{bytes.NewReader([]byte("data"))})
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("upload returned before HTTP request: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP upload did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("upload HTTP request ignored cancellation")
	}
	select {
	case err := <-done:
		var pcsErr pcserror.Error
		if !errors.As(err, &pcsErr) || !errors.Is(pcsErr.GetError(), context.Canceled) {
			t.Fatalf("canceled request lost cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("upload adapter did not return")
	}
}

func TestPCSUploadRejectsTerminalHTTPStatus(t *testing.T) {
	for _, status := range []int{400, 401, 403, 413} {
		pu := testPCSUploadClient(t, func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Status: "synthetic HTTP error", Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error_code":31300}`)), Request: r}, nil
		})
		_, err := pu.TmpFile(context.Background(), "synthetic", "/synthetic", 0, 0, uploadTestReader{bytes.NewReader([]byte("data"))})
		var multiErr *uploader.MultiError
		if !errors.As(err, &multiErr) || !multiErr.Terminated || multiErr.Err == nil {
			t.Fatalf("status %d was not terminal: %v", status, err)
		}
	}
}
