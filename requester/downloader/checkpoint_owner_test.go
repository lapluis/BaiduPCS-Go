package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func checkpointOwnerInfo(begin int64) *transfer.DownloadInstanceInfo {
	status := transfer.NewDownloadStatus()
	status.SetTotalSize(8)
	status.SetRangeListGen(transfer.NewRangeListGenDefault(8, 8, 1, 1))
	return &transfer.DownloadInstanceInfo{DownloadStatus: status, Ranges: transfer.RangeList{{Begin: begin, End: 8}}}
}

func TestCheckpointOwnershipAcrossProcesses(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint")
			owner, err := openCheckpointState(path, format)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if err := owner.Put(checkpointOwnerInfo(6)); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{"busy", "missing", "free"} {
				if phase == "missing" {
					// Model the interval between closing the data handle and
					// releasing the independent lease after checkpoint removal.
					if err := owner.saveFile.Close(); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "free" {
					if err := owner.Close(); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCheckpointOwnerProcessFixture$")
				cmd.Env = append(os.Environ(), "PCS_CHECKPOINT_OWNER_PATH="+path, "PCS_CHECKPOINT_OWNER_PHASE="+phase, "PCS_CHECKPOINT_OWNER_FORMAT="+fmt.Sprint(format))
				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("child phase %s: %v %s", phase, err, out)
				}
				if phase == "missing" {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("contender created state before acquiring lease")
					}
				}
			}
		})
	}
}

func TestCheckpointOwnerProcessFixture(t *testing.T) {
	path := os.Getenv("PCS_CHECKPOINT_OWNER_PATH")
	if path == "" {
		return
	}
	format, err := strconv.Atoi(os.Getenv("PCS_CHECKPOINT_OWNER_FORMAT"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := openCheckpointState(path, InstanceStateStorageFormat(format))
	if os.Getenv("PCS_CHECKPOINT_OWNER_PHASE") != "free" {
		if err == nil {
			state.Close()
			t.Fatal("child acquired another owner's checkpoint")
		}
		if errors.Is(err, ErrResumeFileMismatch) {
			t.Fatal("busy checkpoint classified as corrupt state")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := state.Put(checkpointOwnerInfo(6)); err != nil {
		t.Fatal(err)
	}
	if info, err := state.Load(); err != nil || info == nil || info.Ranges.Len() != 2 {
		t.Fatal("child failed to acquire released checkpoint")
	}
}

func TestCheckpointCleanupOnlyRemovesOwnedPath(t *testing.T) {
	for _, disabled := range []bool{true, false} {
		t.Run(fmt.Sprint(disabled), func(t *testing.T) {
			root := t.TempDir()
			other := filepath.Join(root, "valuable.data")
			if err := os.WriteFile(other, []byte("keep user data"), 0600); err != nil {
				t.Fatal(err)
			}
			state := NewInstanceState(nil, InstanceStateStorageFormatJSON)
			if !disabled {
				state = openOwnedCheckpoint(t, filepath.Join(root, "checkpoint"), InstanceStateStorageFormatJSON)
				if err := state.Put(checkpointOwnerInfo(6)); err != nil {
					t.Fatal(err)
				}
			}
			d := &Downloader{instanceState: state, config: &Config{InstanceStatePath: other}}
			if err := d.removeInstanceState(); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(other); err != nil || string(data) != "keep user data" {
				t.Fatal("cleanup removed unowned config path")
			}
		})
	}
}

func openOwnedCheckpoint(t *testing.T, path string, format InstanceStateStorageFormat) *InstanceState {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(file, format)
	t.Cleanup(func() { state.Close() })
	return state
}

func TestCheckpointSecondOwnerIsRejected(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint")
			first := openOwnedCheckpoint(t, path, format)
			if err := first.Put(checkpointOwnerInfo(4)); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			second := openOwnedCheckpoint(t, path, format)
			if info, err := second.Load(); err == nil || info != nil {
				t.Error("second owner loaded active checkpoint")
			}
			second.Close()
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, before) {
				t.Error("rejected owner changed active record")
			}
			if err := first.Put(checkpointOwnerInfo(6)); err != nil {
				t.Fatal("failed owner blocked active save:", err)
			}
			first.Close()
			fresh := openOwnedCheckpoint(t, path, format)
			if info, err := fresh.Load(); err != nil || info == nil || info.Ranges.Len() != 2 {
				t.Fatalf("latest progress unavailable after owner release: %v %v", info, err)
			}
		})
	}
}

func TestClosedCheckpointCannotRemoveNewerProgress(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "checkpoint")
			stale := openOwnedCheckpoint(t, path, format)
			if err := stale.Put(checkpointOwnerInfo(4)); err != nil {
				t.Fatal(err)
			}
			stale.Close()
			fresh := openOwnedCheckpoint(t, path, format)
			if err := fresh.Put(checkpointOwnerInfo(6)); err != nil {
				t.Fatal(err)
			}
			fresh.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			d := &Downloader{instanceState: stale, config: &Config{InstanceStatePath: path}}
			if err := d.removeInstanceState(); err == nil {
				t.Error("closed owner removed a later checkpoint")
			}
			if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, before) {
				t.Errorf("newer checkpoint lost: %v", err)
			}
		})
	}
}
