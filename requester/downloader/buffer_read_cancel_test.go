package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/rio"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type cancelBufferedRead struct {
	calls     int
	interrupt func()
}

func (r *cancelBufferedRead) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 1 {
		p[0] = 'a'
		r.interrupt()
		return 1, nil
	}
	// Bound a regression without relying on Close to stop this in-memory reader.
	return 0, io.ErrUnexpectedEOF
}

func TestWorkerStopsBufferedReadsWithoutGlobalStatus(t *testing.T) {
	for _, action := range []string{"parent-cancel", "worker-cancel", "pause"} {
		for _, withStatus := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/status=%t", action, withStatus), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				original := bytes.Repeat([]byte{'_'}, 8)
				output := rio.NewBuffer(bytes.Clone(original))
				worker := NewWorker(0, "https://fixture.invalid/file", output)
				worker.SetAcceptRange("bytes")
				worker.SetRange(&transfer.Range{Begin: 0, End: 8})
				worker.SetTotalSize(8)
				worker.cacheSize, worker.parentCtx = 4, ctx
				if withStatus {
					worker.SetDownloadStatus(transfer.NewDownloadStatus())
				}
				reader := &cancelBufferedRead{interrupt: func() {
					switch action {
					case "parent-cancel":
						cancel()
					case "worker-cancel":
						if err := worker.Cancel(); err != nil {
							t.Errorf("cancel failed: %v", err)
						}
					case "pause":
						worker.Pause()
					}
				}}
				worker.firstResp = &http.Response{StatusCode: http.StatusPartialContent, ContentLength: 8,
					Header: http.Header{"Content-Range": {"bytes 0-7/8"}}, Body: io.NopCloser(reader)}
				worker.Execute()
				if reader.calls != 1 {
					t.Errorf("read after interrupt: calls=%d want=1", reader.calls)
				}
				wantStatus := StatusCodeCanceled
				if action == "pause" {
					wantStatus = StatusCodePaused
				}
				if worker.GetStatus().StatusCode() != wantStatus {
					t.Errorf("status=%v want=%v", worker.GetStatus().StatusCode(), wantStatus)
				}
				if action != "pause" && !errors.Is(worker.Err(), context.Canceled) {
					t.Errorf("cancellation lost: %v", worker.Err())
				}
				if action == "pause" && worker.Err() != nil {
					t.Errorf("pause published error: %v", worker.Err())
				}
				if worker.GetRange().LoadBegin() != 0 || !bytes.Equal(output.Bytes(), original) {
					t.Fatal("interrupted buffered bytes were committed")
				}
				if withStatus && worker.downloadStatus.Downloaded() != 0 {
					t.Fatal("interrupted bytes were counted as completed")
				}
			})
		}
	}
}
