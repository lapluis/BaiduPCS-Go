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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadEventPanicRetainsFinalization(t *testing.T) {
	for _, phase := range []string{"normal", "execute", "success", "finish", "execute-finish", "sync-finish", "execute-save-finish"} {
		for _, typed := range []bool{false, true} {
			for _, canceled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/typed=%t/canceled=%t", phase, typed, canceled), func(t *testing.T) {
					t.Parallel()
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						requests.Add(1)
						http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
					}))
					defer server.Close()
					name := filepath.Join(t.TempDir(), "data")
					const original = "original data with tail"
					if err := os.WriteFile(name, []byte(original), 0600); err != nil {
						t.Fatal(err)
					}
					file, err := os.OpenFile(name, os.O_RDWR, 0600)
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					writer := &failingSyncFile{File: file}
					state := name + ".state"
					d := NewDownloader(server.URL, writer, &Config{MaxParallel: 1, InstanceStatePath: state, InstanceStateStorageFormat: InstanceStateStorageFormatProto3})
					d.SetFileContentLength(8)
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					startCause, successCause, finishCause := errors.New("start callback"), errors.New("success callback"), errors.New("finish callback")
					syncCause := errors.New("disk sync failure")
					startFails := strings.HasPrefix(phase, "execute")
					syncFails := phase == "sync-finish" || phase == "execute-save-finish"
					starts, successes, finishes := 0, 0, 0
					fail := func(cause error) {
						if typed {
							panic(cause)
						}
						panic(cause.Error())
					}
					d.OnExecute(func() {
						starts++
						if err := d.monitor.saveCheckpoint(); err != nil {
							t.Errorf("baseline checkpoint: %v", err)
						}
						if syncFails {
							writer.err = syncCause
						}
						if startFails {
							if canceled {
								cancel()
							}
							fail(startCause)
						}
					})
					d.OnSuccess(func() {
						successes++
						if phase == "success" {
							if canceled {
								cancel()
							}
							fail(successCause)
						}
					})
					d.OnFinish(func() {
						finishes++
						select {
						case <-d.monitor.CompletedChan():
						default:
							t.Error("finish preceded monitor completion")
						}
						if canceled {
							cancel()
						}
						if strings.HasSuffix(phase, "finish") {
							fail(finishCause)
						}
					})
					var escaped any
					var runErr error
					func() { defer func() { escaped = recover() }(); runErr = d.ExecuteWithContext(ctx) }()
					if escaped != nil {
						t.Errorf("callback panic escaped: %v", escaped)
					}
					if errors.Is(runErr, ErrEventCallback) != (phase != "normal") {
						t.Errorf("wrong event error classification: %v", runErr)
					}
					if errors.Is(runErr, context.Canceled) != canceled || errors.Is(runErr, syncCause) != syncFails {
						t.Errorf("lost cancellation/disk cause: %v", runErr)
					}
					wantSuccess := 1
					if startFails || syncFails {
						wantSuccess = 0
					}
					if starts != 1 || successes != wantSuccess || finishes != 1 {
						t.Errorf("events start=%d success=%d finish=%d", starts, successes, finishes)
					}
					for _, item := range []struct {
						want  bool
						cause error
					}{{startFails, startCause}, {phase == "success", successCause}, {strings.HasSuffix(phase, "finish"), finishCause}} {
						if item.want && (runErr == nil || !strings.Contains(runErr.Error(), item.cause.Error()) || typed && !errors.Is(runErr, item.cause)) {
							t.Errorf("lost callback cause %v: %v", item.cause, runErr)
						}
					}
					if phase == "normal" && !canceled && runErr != nil {
						t.Errorf("normal download: %v", runErr)
					}
					wantData := "abcdefgh"
					if startFails {
						wantData = original
						if requests.Load() != 0 {
							t.Errorf("startup failure sent %d requests", requests.Load())
						}
					}
					if got, err := os.ReadFile(name); err != nil || string(got) != wantData {
						t.Errorf("data=%q err=%v, want %q", got, err, wantData)
					}
					if startFails || syncFails {
						saved, err := os.Open(state)
						if err != nil {
							t.Fatal("checkpoint lost:", err)
						}
						reader := NewInstanceState(saved, InstanceStateStorageFormatProto3)
						defer reader.Close()
						info, err := reader.Load()
						if err != nil || info == nil || info.DownloadStatus.Downloaded() != 0 || info.Ranges.Len() != 8 {
							t.Errorf("baseline checkpoint changed: info=%v err=%v", info, err)
						}
					} else if _, err := os.Stat(state); !os.IsNotExist(err) {
						t.Errorf("completed download retained checkpoint: %v", err)
					}
				})
			}
		}
	}
}
