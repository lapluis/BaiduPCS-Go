package downloader

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

type blockingCheckpointSnapshot struct {
	*transfer.DownloadInstanceInfoExport
	started, release chan struct{}
}

func (b *blockingCheckpointSnapshot) SetInstanceInfo(info *transfer.DownloadInstanceInfo) {
	b.DownloadInstanceInfoExport.SetInstanceInfo(info)
	close(b.started)
	<-b.release
}

func TestCheckpointCloseWaitsForSaveAndPreservesRecord(t *testing.T) {
	name := filepath.Join(t.TempDir(), "checkpoint")
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(file, InstanceStateStorageFormatJSON)
	defer state.Close()
	snapshot := &blockingCheckpointSnapshot{DownloadInstanceInfoExport: &transfer.DownloadInstanceInfoExport{}, started: make(chan struct{}), release: make(chan struct{})}
	state.ii = snapshot
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	saved := make(chan error, 1)
	go func() {
		saved <- state.Put(&transfer.DownloadInstanceInfo{DownloadStatus: status, Ranges: transfer.RangeList{&transfer.Range{Begin: 0, End: 8}}})
	}()
	<-snapshot.started
	closed := make(chan error, 1)
	go func() { closed <- state.Close() }()
	var closeErr error
	returnedEarly := false
	select {
	case closeErr = <-closed:
		returnedEarly = true
		t.Error("checkpoint closed while a save still used its file")
	case <-time.After(50 * time.Millisecond):
	}
	close(snapshot.release)
	if err := <-saved; err != nil {
		t.Error("in-flight save failed:", err)
	}
	if !returnedEarly {
		closeErr = <-closed
	}
	if closeErr != nil {
		t.Error("close:", closeErr)
	}
	if _, err := state.saveFile.Stat(); err == nil {
		t.Error("close left the replacement file handle open")
	}
	if err := state.Close(); err != nil {
		t.Error("repeated close:", err)
	}
	reader, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reloaded := NewInstanceState(reader, InstanceStateStorageFormatJSON)
	defer reloaded.Close()
	loaded, err := reloaded.Load()
	if err != nil || loaded == nil {
		t.Fatalf("saved checkpoint lost: info=%v err=%v", loaded, err)
	}
	if loaded.DownloadStatus.TotalSize() != 8 || loaded.Ranges.Len() != 8 {
		t.Fatalf("saved checkpoint changed: %+v", loaded)
	}
}
