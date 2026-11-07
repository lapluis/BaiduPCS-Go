package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type progressPanicSyncWriter struct {
	*os.File
	fail  atomic.Bool
	cause error
}

func (w *progressPanicSyncWriter) Sync() error {
	if w.fail.Load() {
		return w.cause
	}
	return w.File.Sync()
}

func TestProgressPanicStopsWorkersAndPreservesResume(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, phase := range []string{"typed", "string", "checkpoint", "late", "canceled", "single"} {
			t.Run(fmt.Sprintf("%d/%s", format, phase), func(t *testing.T) {
				t.Parallel()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProgressPanicProcessFixture$")
				cmd.Env = append(os.Environ(), "PCS_PROGRESS_PANIC_FIXTURE="+fmt.Sprintf("%d/%s", format, phase))
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("progress callback killed or failed the child: %v %s", err, output)
				}
			})
		}
	}
}

func TestProgressPanicProcessFixture(t *testing.T) {
	fixture := os.Getenv("PCS_PROGRESS_PANIC_FIXTURE")
	if fixture == "" {
		return
	}
	parts := strings.SplitN(fixture, "/", 2)
	if len(parts) != 2 {
		t.Fatal("invalid fixture")
	}
	formatValue, err := strconv.Atoi(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	format, phase := InstanceStateStorageFormat(formatValue), parts[1]
	content := bytes.Repeat([]byte("abcdefgh"), 8192)
	const initial = 16384
	root := t.TempDir()
	path, statePath := filepath.Join(root, "data"), filepath.Join(root, "state")
	if err := os.WriteFile(path, content[:initial], 0600); err != nil {
		t.Fatal(err)
	}
	state := openOwnedCheckpoint(t, statePath, format)
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(int64(len(content)))
	status.SetRangeListGen(transfer.NewRangeListGenDefault(int64(len(content)), int64(len(content)), 1, 1))
	if err := state.Put(&transfer.DownloadInstanceInfo{DownloadStatus: status, Ranges: transfer.RangeList{{Begin: initial, End: int64(len(content))}}}); err != nil {
		t.Fatal(err)
	}
	state.Close()
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stopped, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	stopOnce := sync.OnceFunc(func() { close(stopped) })
	var retrying atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retrying.Load() {
			http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
			return
		}
		begin := initial
		if phase == "single" {
			begin = 0
		}
		if phase != "single" && r.Header.Get("Range") != fmt.Sprintf("bytes=%d-%d", initial, len(content)-1) {
			t.Errorf("initial resume request: %s", r.Header.Get("Range"))
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(content)-begin))
		if phase == "single" {
			w.Header().Set("ETag", `W/"fixture"`)
			w.WriteHeader(200)
		} else {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, len(content)-1, len(content)))
			w.WriteHeader(206)
		}
		_, _ = w.Write(content[begin : 2*initial])
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
			_, _ = w.Write(content[2*initial:])
		}
		stopOnce()
	}))
	defer server.Close()
	defer releaseOnce()
	syncCause := errors.New("synthetic progress checkpoint flush failure")
	writer := &progressPanicSyncWriter{File: file, cause: syncCause}
	config := &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath, InstanceStateStorageFormat: format}
	d := NewDownloader(server.URL, writer, config)
	d.SetFileContentLength(int64(len(content)))
	if phase == "single" {
		d.SetFirstInfo(&DownloadFirstInfo{ContentLength: int64(len(content)), AcceptRanges: DefaultAcceptRanges, ETag: `W/"fixture"`})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	panicCause := errors.New("synthetic progress callback panic")
	exited := make(chan struct{})
	var calls atomic.Int32
	d.OnDownloadStatusEvent(func(transfer.DownloadStatuser, func(RangeWorkerFunc)) {
		calls.Add(1)
		defer close(exited)
		if phase == "late" {
			releaseOnce()
			<-d.monitor.CompletedChan()
		}
		if phase == "checkpoint" {
			writer.fail.Store(true)
		}
		if phase == "canceled" {
			cancel()
		}
		if phase == "string" {
			panic(panicCause.Error())
		}
		panic(panicCause)
	})
	success, finished := false, false
	d.OnSuccess(func() { success = true })
	d.OnFinish(func() {
		finished = true
		select {
		case <-exited:
		default:
			t.Error("finish preceded progress exit")
		}
	})
	err = d.ExecuteWithContext(ctx)
	if err == nil || !strings.Contains(err.Error(), panicCause.Error()) || (phase != "string" && !errors.Is(err, panicCause)) {
		t.Fatalf("progress panic not returned: %v", err)
	}
	if errors.Is(err, context.Canceled) != (phase == "canceled") || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("callback failure disguised as user cancellation: %v", err)
	}
	if errors.Is(err, ErrCheckpointSave) != (phase == "checkpoint") || errors.Is(err, syncCause) != (phase == "checkpoint") {
		t.Errorf("checkpoint failure classification: %v", err)
	}
	if success || !finished || calls.Load() != 1 {
		t.Errorf("wrong lifecycle: success=%t finish=%t calls=%d", success, finished, calls.Load())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("progress failure left request running")
	}
	partial, err := os.ReadFile(path)
	if err != nil || len(partial) < initial || len(partial) > len(content) || !bytes.Equal(partial, content[:len(partial)]) {
		t.Fatalf("partial data damaged: size=%d err=%v", len(partial), err)
	}
	saved := openOwnedCheckpoint(t, statePath, format)
	info, err := saved.Load()
	if err != nil || (info == nil) != (phase == "single") {
		t.Fatalf("progress failure lost checkpoint: %v", err)
	}
	saved.Close()
	writer.fail.Store(false)
	retrying.Store(true)
	retry := NewDownloader(server.URL, file, config)
	retry.SetFileContentLength(int64(len(content)))
	if err := retry.Execute(); err != nil {
		t.Fatal("resume after progress failure:", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, content) {
		t.Fatal("resumed output mismatch:", err)
	}
}
