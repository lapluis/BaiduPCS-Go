package downloader

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestCheckpointSaveAfterLoadDropsStaleGenerator(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			file, err := os.Create(filepath.Join(t.TempDir(), "checkpoint"))
			if err != nil {
				t.Fatal(err)
			}
			state := NewInstanceState(file, format)
			defer state.Close()
			old := transfer.NewDownloadStatus()
			old.SetTotalSize(8)
			old.SetRangeListGen(transfer.NewRangeListGenBlockSize(8, 8, 4))
			if err := state.Put(&transfer.DownloadInstanceInfo{DownloadStatus: old, Ranges: transfer.RangeList{{Begin: 4, End: 8}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := state.Load(); err != nil {
				t.Fatal(err)
			}
			fresh := transfer.NewDownloadStatus()
			fresh.SetTotalSize(3)
			if err := state.Put(&transfer.DownloadInstanceInfo{DownloadStatus: fresh, Ranges: transfer.RangeList{{Begin: 1, End: 3}}}); err != nil {
				t.Fatal(err)
			}
			loaded, err := state.Load()
			if err != nil || loaded == nil {
				t.Fatalf("new checkpoint cannot be restored: info=%v err=%v", loaded, err)
			}
			if loaded.DownloadStatus.TotalSize() != 3 || loaded.DownloadStatus.Downloaded() != 1 || loaded.Ranges.Len() != 2 || loaded.DownloadStatus.RangeListGen().RangeGenMode() != transfer.RangeGenMode_Default {
				t.Fatalf("new checkpoint kept stale generator: %+v", loaded)
			}
		})
	}
}
