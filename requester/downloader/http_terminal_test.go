package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTerminalHTTPFailureStopsAndRetainsResumableData(t *testing.T) {
	for _, status := range []int{400, 401, 405, 410} {
		for _, ranged := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/ranged=%v", status, ranged), func(t *testing.T) {
				entityTag := `"file-entity"`
				if !ranged {
					entityTag = `W/"file-entity"`
				}
				var requests atomic.Int32
				var reject atomic.Bool
				reject.Store(true)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					wantRange := ""
					if ranged {
						wantRange = "bytes=4-7"
					}
					if r.Header.Get("Range") != wantRange {
						t.Errorf("request lost pending range: got=%q want=%q", r.Header.Get("Range"), wantRange)
					}
					if reject.Load() {
						w.Header().Set("ETag", `"error-document"`)
						http.Error(w, "synthetic rejected request", status)
						return
					}
					w.Header().Set("ETag", entityTag)
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
				}))
				defer server.Close()
				root := t.TempDir()
				statePath, dataPath := filepath.Join(root, "state"), filepath.Join(root, "data")
				if ranged {
					state := openOwnedCheckpoint(t, statePath, InstanceStateStorageFormatJSON)
					if err := state.Put(checkpointOwnerInfo(4)); err != nil {
						t.Fatal(err)
					}
					if err := state.Close(); err != nil {
						t.Fatal(err)
					}
				}
				original := []byte("abcd????")
				if err := os.WriteFile(dataPath, original, 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(dataPath, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				config := &Config{MaxParallel: 1, InstanceStatePath: statePath, InstanceStateStorageFormat: InstanceStateStorageFormatJSON}
				info := &DownloadFirstInfo{ContentLength: 8, ETag: entityTag}
				if ranged {
					info.AcceptRanges = "bytes"
				}
				d := NewDownloader(server.URL, file, config)
				d.SetFirstInfo(info)
				succeeded, finished := false, 0
				d.OnSuccess(func() { succeeded = true })
				d.OnFinish(func() { finished++ })
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				err = d.ExecuteWithContext(ctx)
				if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRemoteChanged) || !strings.Contains(err.Error(), strconv.Itoa(status)) || ctx.Err() != nil || requests.Load() != 1 || succeeded || finished != 1 {
					t.Fatalf("HTTP rejection retried or lost: requests=%d success=%v finished=%d err=%v", requests.Load(), succeeded, finished, err)
				}
				if got, err := os.ReadFile(dataPath); err != nil || !bytes.Equal(got, original) {
					t.Fatalf("error document changed output: %q %v", got, err)
				}
				if ranged {
					state := openOwnedCheckpoint(t, statePath, InstanceStateStorageFormatJSON)
					saved, loadErr := state.Load()
					closeErr := state.Close()
					if loadErr != nil || closeErr != nil || saved == nil || saved.DownloadStatus.TotalSize() != 8 || len(saved.Ranges) != 1 || saved.Ranges[0].LoadBegin() != 4 || saved.Ranges[0].LoadEnd() != 8 {
						t.Fatalf("pending checkpoint lost: saved=%+v load=%v close=%v", saved, loadErr, closeErr)
					}
				}
				reject.Store(false)
				retry := NewDownloader(server.URL, file, config)
				retry.SetFirstInfo(info)
				if err := retry.ExecuteWithContext(ctx); err != nil {
					t.Fatalf("recovered request failed: %v", err)
				}
				if got, err := os.ReadFile(dataPath); err != nil || string(got) != "abcdefgh" || requests.Load() != 2 {
					t.Fatalf("resume did not restore payload: got=%q requests=%d err=%v", got, requests.Load(), err)
				}
			})
		}
	}
}
