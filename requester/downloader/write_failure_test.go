package downloader

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type failingSyncFile struct {
	*os.File
	err    error
	called atomic.Bool
}

func (f *failingSyncFile) Sync() error { f.called.Store(true); return f.err }

func TestDownloadSyncFailureCannotSucceedOrDiscardCheckpoint(t *testing.T) {
	content := []byte("downloaded-data")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	wantErr := errors.New("injected disk flush failure")
	writer := &failingSyncFile{File: f, err: wantErr}
	state := f.Name() + ".state"
	d := NewDownloader(server.URL, writer, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: state})
	d.SetFileContentLength(int64(len(content)))
	var success atomic.Bool
	d.OnSuccess(func() { success.Store(true) })
	if err := d.Execute(); !errors.Is(err, wantErr) || !writer.called.Load() || success.Load() {
		t.Fatalf("unflushed data accepted: err=%v sync=%v success=%v", err, writer.called.Load(), success.Load())
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("sync fixture did not complete buffered writes", err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("failed flush discarded checkpoint", err)
	}
	writer.err = nil
	writer.called.Store(false)
	d = NewDownloader(server.URL, writer, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: state})
	d.SetFileContentLength(int64(len(content)))
	if err := d.Execute(); err != nil || !writer.called.Load() {
		t.Fatalf("retry did not flush successfully: %v", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("successful retry retained checkpoint")
	}
}

type shortDownloadWriter struct{}

func (shortDownloadWriter) WriteAt(p []byte, _ int64) (int, error) { return len(p) / 2, nil }

func TestDownloadShortWriteDoesNotAdvanceRangeOrProgress(t *testing.T) {
	worker := NewWorker(0, "", shortDownloadWriter{})
	worker.SetAcceptRange("bytes")
	worker.SetTotalSize(8)
	worker.SetRange(&transfer.Range{Begin: 0, End: 8})
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	worker.SetDownloadStatus(status)
	worker.firstResp = &http.Response{StatusCode: 206, ContentLength: 8, Header: http.Header{"Content-Range": []string{"bytes 0-7/8"}}, Body: io.NopCloser(bytes.NewReader([]byte("12345678")))}
	worker.Execute()
	if !errors.Is(worker.Err(), io.ErrShortWrite) || worker.GetStatus().StatusCode() != StatusCodeInternalError || worker.GetRange().LoadBegin() != 0 || status.Downloaded() != 0 {
		t.Fatalf("short write accepted: err=%v begin=%d downloaded=%d", worker.Err(), worker.GetRange().LoadBegin(), status.Downloaded())
	}
}
