package downloader

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
	"golang.org/x/sys/windows"
)

func TestCheckpointReplacementFailureRetainsPreviousRecord(t *testing.T) {
	name := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(name, []byte("keep old checkpoint"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(f, InstanceStateStorageFormatJSON)
	defer state.Close()
	path, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	// Permit normal read/write operations but block replacement of this file.
	h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocker := os.NewFile(uintptr(h), name)
	defer blocker.Close()
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	status.SetRangeListGen(transfer.NewRangeListGenDefault(8, 8, 1, 1))
	info := &transfer.DownloadInstanceInfo{DownloadStatus: status, Ranges: transfer.RangeList{{Begin: 4, End: 8}}}
	if err := state.Put(info); !errors.Is(err, ErrCheckpointSave) {
		t.Fatalf("replacement failure not reported: %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil || string(got) != "keep old checkpoint" {
		t.Fatalf("old checkpoint overwritten before publication: %q %v", got, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(name), ".pcs-download-state-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("failed replacement leaked temporary file", leftovers, err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.Put(info); err != nil {
		t.Fatal("retry failed after releasing replacement lock:", err)
	}
	loaded := state.Get()
	if loaded == nil || loaded.DownloadStatus.TotalSize() != 8 || loaded.Ranges.Len() != 4 {
		t.Fatal("retry did not preserve new checkpoint contents")
	}
}

type checkpointCleanupLockWriter struct {
	*os.File
	lockCleanup func() error
}

func (w *checkpointCleanupLockWriter) Truncate(size int64) error {
	if err := w.File.Truncate(size); err != nil {
		return err
	}
	return w.lockCleanup()
}

func TestDownloadCannotSucceedWhenCheckpointCleanupFails(t *testing.T) {
	content := []byte("complete")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	statePath := f.Name() + ".state"
	if err := os.WriteFile(statePath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var blocker *os.File
	defer func() {
		if blocker != nil {
			blocker.Close()
		}
	}()
	// Truncate runs after workers and checkpoint saves have stopped. Lock only
	// here so this test cannot fail early on checkpoint replacement instead.
	writer := &checkpointCleanupLockWriter{File: f, lockCleanup: func() error {
		path, err := windows.UTF16PtrFromString(statePath)
		if err != nil {
			return err
		}
		h, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			return err
		}
		blocker = os.NewFile(uintptr(h), statePath)
		return nil
	}}
	d := NewDownloader(server.URL, writer, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath})
	d.SetFileContentLength(8)
	var checkpointErr error
	d.OnExecute(func() { checkpointErr = d.monitor.saveCheckpoint() })
	var success, finish atomic.Bool
	d.OnSuccess(func() { success.Store(true) })
	d.OnFinish(func() { finish.Store(true) })
	err = d.Execute()
	if checkpointErr != nil {
		t.Fatal("ordinary checkpoint save failed before cleanup:", checkpointErr)
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "remove" || success.Load() || !finish.Load() {
		t.Fatalf("cleanup error hidden by success: err=%v success=%v finish=%v", err, success.Load(), finish.Load())
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal("blocked checkpoint unexpectedly removed", err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("cleanup failure damaged payload", err)
	}
	if err := blocker.Close(); err != nil {
		t.Fatal(err)
	}
	d = NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath})
	d.SetFileContentLength(8)
	if err := d.Execute(); err != nil {
		t.Fatal("cleanup retry failed:", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatal("retry did not clean checkpoint")
	}
}
