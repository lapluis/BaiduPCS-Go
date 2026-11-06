package downloader

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestCompletedCheckpointFinishesWithoutRedownloading(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, mode := range []transfer.RangeGenMode{transfer.RangeGenMode_Default, transfer.RangeGenMode_BlockSize} {
			for _, failSync := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%d/%v", format, mode, failSync), func(t *testing.T) {
					var requests atomic.Int64
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						http.Error(w, "unexpected download", 403)
					}))
					defer server.Close()
					dir := t.TempDir()
					name, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
					content := []byte("complete")
					if err := os.WriteFile(name, content, 0600); err != nil {
						t.Fatal(err)
					}
					exported := &transfer.DownloadInstanceInfoExport{TotalSize: 8, GenBegin: 8, BlockSize: 4, RangeGenMode: mode}
					var state []byte
					var err error
					if format == InstanceStateStorageFormatProto3 {
						state, err = proto.Marshal(exported)
					} else {
						state, err = json.Marshal(exported)
					}
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(statePath, state, 0600); err != nil {
						t.Fatal(err)
					}
					f, err := os.OpenFile(name, os.O_RDWR, 0600)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					writer := &failingSyncFile{File: f}
					wantErr := errors.New("injected final flush failure")
					if failSync {
						writer.err = wantErr
					}
					d := NewDownloader(server.URL, writer, &Config{MaxParallel: 4, CacheSize: 1024, InstanceStatePath: statePath, InstanceStateStorageFormat: format})
					d.SetFileContentLength(8)
					var success atomic.Bool
					d.OnSuccess(func() { success.Store(true) })
					err = d.Execute()
					if failSync {
						if !errors.Is(err, wantErr) || success.Load() {
							t.Fatalf("final flush failure lost: err=%v success=%v", err, success.Load())
						}
						if _, err := os.Stat(statePath); err != nil {
							t.Fatal("failed finalization lost checkpoint", err)
						}
					} else {
						if err != nil || !success.Load() {
							t.Fatalf("completed checkpoint rejected: err=%v success=%v", err, success.Load())
						}
						if _, err := os.Stat(statePath); !os.IsNotExist(err) {
							t.Fatal("completed checkpoint not cleaned")
						}
					}
					if !writer.called.Load() || requests.Load() != 0 {
						t.Fatalf("wrong finalization: flushed=%v requests=%d", writer.called.Load(), requests.Load())
					}
					got, err := os.ReadFile(name)
					if err != nil || !bytes.Equal(got, content) {
						t.Fatal("completed payload changed", err)
					}
				})
			}
		}
	}
}

func TestNoWorkersCannotHideUnfinishedWork(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		downloaded, generated int64
		pending               bool
	}{
		{"missing-bytes", 0, 8, false},
		{"unassigned-tail", 8, 4, false},
		{"queued-range", 8, 8, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := transfer.NewDownloadStatus()
			status.SetTotalSize(8)
			status.AddDownloaded(tc.downloaded)
			status.SetRangeListGen(transfer.NewRangeListGenBlockSize(8, tc.generated, 4))
			monitor := NewMonitor()
			monitor.SetStatus(status)
			if tc.pending {
				monitor.pendingRanges = transfer.RangeList{{Begin: 4, End: 8}}
			}
			monitor.Execute(context.Background())
			if !errors.Is(monitor.Err(), ErrNoWokers) {
				t.Fatalf("unfinished work reported complete: %v", monitor.Err())
			}
		})
	}
}
