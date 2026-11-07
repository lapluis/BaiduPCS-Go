package downloader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestCheckpointPublishRechecksOwnership(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, change := range []string{"unchanged", "replaced", "removed"} {
			t.Run(fmt.Sprintf("%d/%s", format, change), func(t *testing.T) {
				name := filepath.Join(t.TempDir(), "checkpoint")
				file, err := openReplaceableCheckpointFile(name)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				initial := &transfer.DownloadInstanceInfoExport{}
				initial.SetInstanceInfo(checkpointOwnerInfo(4))
				original, err := json.Marshal(initial)
				if format == InstanceStateStorageFormatProto3 {
					original, err = proto.Marshal(initial)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(original); err != nil {
					t.Fatal(err)
				}
				state := NewInstanceState(file, format)
				defer state.Close()
				snapshot := &blockingCheckpointSnapshot{DownloadInstanceInfoExport: &transfer.DownloadInstanceInfoExport{}, started: make(chan struct{}), release: make(chan struct{})}
				state.ii = snapshot
				release := sync.OnceFunc(func() { close(snapshot.release) })
				done := make(chan error, 1)
				go func() { done <- state.Put(checkpointOwnerInfo(6)) }()
				joined := false
				defer func() {
					release()
					if !joined {
						<-done
					}
				}()
				select {
				case <-snapshot.started:
				case <-time.After(time.Second):
					t.Fatal("save did not reach snapshot preparation")
				}
				backup := name + ".owned"
				if change != "unchanged" {
					if err := os.Rename(name, backup); err != nil {
						t.Fatal("replace owned pathname", err)
					}
					if change == "replaced" {
						if err := os.WriteFile(name, []byte("external record"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				release()
				err = <-done
				joined = true
				if change == "unchanged" {
					if err != nil {
						t.Fatal("ordinary save failed", err)
					}
					state.ii = nil
					loaded, err := state.Load()
					if err != nil || loaded == nil || loaded.DownloadStatus.Downloaded() != 6 {
						t.Fatal("ordinary save lost progress", loaded, err)
					}
				} else {
					cause := ErrCheckpointConflict
					if change == "removed" {
						cause = os.ErrNotExist
					}
					if !errors.Is(err, ErrCheckpointSave) || !errors.Is(err, cause) {
						t.Errorf("changed pathname accepted: %v", err)
					}
					data, readErr := os.ReadFile(backup)
					if readErr != nil || !bytes.Equal(data, original) {
						t.Error("original owned record changed", readErr)
					}
					data, readErr = os.ReadFile(name)
					if change == "replaced" && (readErr != nil || string(data) != "external record") {
						t.Errorf("replacement overwritten: %q %v", data, readErr)
					}
					if change == "removed" && !os.IsNotExist(readErr) {
						t.Error("removed path recreated", string(data), readErr)
					}
				}
				leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(name), ".pcs-download-state-*"))
				if err != nil || len(leftovers) != 0 {
					t.Fatal("temporary state leaked", leftovers, err)
				}
			})
		}
	}
}
