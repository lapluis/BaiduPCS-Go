package downloader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestCheckpointRejectsInvalidSnapshotsBeforeReplacingRecord(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, mode := range []string{"nil-info", "nil-status", "negative-size", "nil-range", "negative-range", "reversed-range", "past-end", "overlap", "past-generated", "invalid-generated", "oversized", "healthy", "empty"} {
			if mode == "oversized" && format != InstanceStateStorageFormatJSON {
				continue
			}
			t.Run(fmt.Sprintf("%d/%s", format, mode), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "state")
				file, err := os.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				state := NewInstanceState(file, format)
				defer state.Close()
				oldStatus := transfer.NewDownloadStatus()
				oldStatus.SetTotalSize(8)
				if err := state.Put(&transfer.DownloadInstanceInfo{DownloadStatus: oldStatus, Ranges: transfer.RangeList{{Begin: 4, End: 8}}}); err != nil {
					t.Fatal(err)
				}
				before, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				beforeMemory, err := json.Marshal(state.ii)
				if err != nil {
					t.Fatal(err)
				}
				status := transfer.NewDownloadStatus()
				status.SetTotalSize(8)
				snapshot := &transfer.DownloadInstanceInfo{DownloadStatus: status, Ranges: transfer.RangeList{{Begin: 2, End: 8}}}
				switch mode {
				case "nil-info":
					snapshot = nil
				case "nil-status":
					snapshot.DownloadStatus = nil
				case "negative-size":
					status.SetTotalSize(-1)
				case "nil-range":
					snapshot.Ranges = transfer.RangeList{nil}
				case "negative-range":
					snapshot.Ranges = transfer.RangeList{{Begin: -1, End: 8}}
				case "reversed-range":
					snapshot.Ranges = transfer.RangeList{{Begin: 5, End: 4}}
				case "past-end":
					snapshot.Ranges = transfer.RangeList{{Begin: 2, End: 9}}
				case "overlap":
					snapshot.Ranges = transfer.RangeList{{Begin: 2, End: 6}, {Begin: 4, End: 8}}
				case "past-generated":
					status.SetRangeListGen(transfer.NewRangeListGenBlockSize(8, 4, 2))
				case "invalid-generated":
					status.SetRangeListGen(transfer.NewRangeListGenBlockSize(8, 9, 2))
				case "oversized":
					completed := &transfer.Range{Begin: 8, End: 8}
					snapshot.Ranges = make(transfer.RangeList, int(maxCheckpointSize/16)+1)
					for i := range snapshot.Ranges {
						snapshot.Ranges[i] = completed
					}
				case "empty":
					status.SetTotalSize(0)
					snapshot.Ranges = nil
				}
				err = state.Put(snapshot)
				valid := mode == "healthy" || mode == "empty"
				if valid {
					if err != nil {
						t.Fatalf("valid snapshot rejected: %v", err)
					}
				} else {
					if !errors.Is(err, ErrCheckpointSave) || !errors.Is(err, ErrResumeFileMismatch) {
						t.Errorf("invalid snapshot accepted or cause lost: %v", err)
					}
					if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, before) {
						t.Errorf("invalid snapshot replaced old record: bytes=%d want=%d error=%v", len(got), len(before), err)
					}
					memory, err := json.Marshal(state.ii)
					if err != nil || !bytes.Equal(memory, beforeMemory) {
						t.Errorf("invalid snapshot changed in-memory record: bytes=%d want=%d error=%v", len(memory), len(beforeMemory), err)
					}
				}
				loaded, loadErr := state.Load()
				if mode == "empty" && format == InstanceStateStorageFormatProto3 && loaded == nil && loadErr == nil {
					if data, err := os.ReadFile(name); err != nil || len(data) != 0 {
						t.Fatalf("empty protobuf checkpoint not reset: bytes=%d error=%v", len(data), err)
					}
					return
				}
				if loadErr != nil || loaded == nil || loaded.DownloadStatus == nil {
					t.Fatalf("stored checkpoint cannot be loaded: %+v %v", loaded, loadErr)
				}
				wantSize, wantRemaining := int64(8), int64(4)
				if mode == "healthy" {
					wantRemaining = 6
				}
				if mode == "empty" {
					wantSize, wantRemaining = 0, 0
				}
				if loaded.DownloadStatus.TotalSize() != wantSize || loaded.Ranges.Len() != wantRemaining {
					t.Errorf("wrong stored progress after save: size=%d remaining=%d", loaded.DownloadStatus.TotalSize(), loaded.Ranges.Len())
				}
			})
		}
	}
}
