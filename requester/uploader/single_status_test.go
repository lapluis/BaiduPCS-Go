package uploader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
)

type singleStatusTransport struct{ started, release chan struct{} }

func (tr *singleStatusTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	close(tr.started)
	<-tr.release
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok")), Request: r}, nil
}

func TestSingleUploadStatusClosesPromptlyAfterCompletion(t *testing.T) {
	tr := &singleStatusTransport{started: make(chan struct{}), release: make(chan struct{})}
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader([]byte("payload"))})
	client := requester.NewHTTPClient()
	client.Client.Transport = tr
	u.SetClient(client)
	u.SetCheckFunc(func(resp *http.Response, err error) {
		if err != nil {
			t.Errorf("upload: %v", err)
		}
		if resp != nil {
			resp.Body.Close()
		}
	})
	status := u.GetStatusChan()
	done := make(chan struct{})
	go func() { u.Execute(); close(done) }()
	<-tr.started
	close(tr.release)
	<-done
	for _, ch := range []<-chan Status{status, u.GetStatusChan()} {
		select {
		case _, ok := <-ch:
			if ok {
				t.Fatal("unexpected status for completed fast upload")
			}
		case <-time.After(200 * time.Millisecond):
			t.Fatal("status stream did not close promptly after upload completed")
		}
	}
}

func TestSingleUploadStatusStopsWithUnreadUpdate(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader([]byte("payload"))})
	u.begin()
	ticks := make(chan time.Time)
	status := make(chan Status)
	done := make(chan struct{})
	go func() { defer close(done); u.streamStatus(status, ticks) }()
	// Receiving this tick puts the producer on the unconsumed status send.
	ticks <- time.Now()
	close(u.finished)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("status producer remained blocked after upload finished")
	}
}

func TestSingleUploadStatusReportsProgress(t *testing.T) {
	u := NewUploader("fixture://upload/", resumeReader{bytes.NewReader([]byte("payload"))})
	u.begin()
	ticks := make(chan time.Time)
	status := make(chan Status)
	done := make(chan struct{})
	go func() { defer close(done); u.streamStatus(status, ticks) }()
	defer func() { close(u.finished); <-done }()
	ticks <- time.Now()
	first := <-status
	if first.Uploaded() != 0 || first.TotalSize() != 7 || first.SpeedsPerSecond() != 0 {
		t.Fatalf("initial snapshot: %+v", first)
	}
	if n, err := u.readed64.Read(make([]byte, 3)); n != 3 || err != nil {
		t.Fatalf("read: n=%d err=%v", n, err)
	}
	ticks <- time.Now()
	next := <-status
	if next.Uploaded() != 3 || next.TotalSize() != 7 || next.SpeedsPerSecond() != 3 || next.TimeElapsed() < 0 {
		t.Fatalf("progress snapshot: %+v", next)
	}
}

func TestSingleUploadStatusKeepsOriginalFileSize(t *testing.T) {
	for _, prefix := range []int64{0, 2} {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload")
			if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			reader := rio.NewFileReaderLen64(file)
			u := NewUploader("fixture://upload/", reader)
			// The size belongs to the body remaining at Execute, even if the caller
			// consumed a prefix after constructing the uploader.
			if _, err := io.CopyN(io.Discard, reader, prefix); err != nil {
				t.Fatal(err)
			}
			u.begin()
			wantTotal := int64(7) - prefix
			ticks := make(chan time.Time)
			status := make(chan Status)
			done := make(chan struct{})
			go func() { defer close(done); u.streamStatus(status, ticks) }()
			defer func() { close(u.finished); <-done }()
			for _, wantRead := range []int64{0, 3, wantTotal} {
				if delta := wantRead - u.readed64.Readed(); delta > 0 {
					if n, err := io.CopyN(io.Discard, u.readed64, delta); n != delta || err != nil {
						t.Fatalf("read: n=%d err=%v", n, err)
					}
				}
				ticks <- time.Now()
				snapshot := <-status
				if snapshot.Uploaded() != wantRead || snapshot.TotalSize() != wantTotal {
					t.Fatalf("file progress changed total: uploaded=%d total=%d wantUploaded=%d", snapshot.Uploaded(), snapshot.TotalSize(), wantRead)
				}
			}
		})
	}
}
