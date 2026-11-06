package downloader

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestInvalidCheckpointDoesNotModifyPayload(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, mode := range []string{"unknown-mode", "overlapping-ranges", "invalid-generated", "invalid-block-size", "malformed"} {
			t.Run(fmt.Sprintf("%d/%s", format, mode), func(t *testing.T) {
				content := bytes.Repeat([]byte("a"), 32)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
				}))
				defer server.Close()
				dir := t.TempDir()
				name, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
				original := bytes.Repeat([]byte("x"), 32)
				if err := os.WriteFile(name, original, 0600); err != nil {
					t.Fatal(err)
				}
				exported := &transfer.DownloadInstanceInfoExport{TotalSize: 32, GenBegin: 32, Ranges: transfer.RangeList{{Begin: 0, End: 8}}}
				switch mode {
				case "unknown-mode":
					exported.RangeGenMode = transfer.RangeGenMode(99)
				case "overlapping-ranges":
					exported.Ranges = transfer.RangeList{{Begin: 0, End: 24}, {Begin: 16, End: 32}}
				case "invalid-generated":
					exported.GenBegin = 33
				case "invalid-block-size":
					exported.RangeGenMode = transfer.RangeGenMode_BlockSize
				}
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
				if mode == "malformed" {
					state = []byte("{broken")
				}
				if err := os.WriteFile(statePath, state, 0600); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(name, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				d := NewDownloader(server.URL, f, &Config{MaxParallel: 2, CacheSize: 1024, InstanceStatePath: statePath, InstanceStateStorageFormat: format})
				if err := d.Execute(); !errors.Is(err, ErrResumeFileMismatch) {
					t.Fatalf("invalid checkpoint accepted: %v", err)
				}
				got, err := os.ReadFile(name)
				if err != nil || !bytes.Equal(got, original) {
					t.Fatal("invalid checkpoint changed payload", err)
				}
				got, err = os.ReadFile(statePath)
				if err != nil || !bytes.Equal(got, state) {
					t.Fatal("invalid checkpoint replaced before being rejected", err)
				}
			})
		}
	}
}

func TestCheckpointLoadBoundsAndReadErrors(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		t.Run(fmt.Sprint(oversized), func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			state := NewInstanceState(f, InstanceStateStorageFormatJSON)
			defer state.Close()
			if oversized {
				if err := f.Truncate(maxCheckpointSize + 1); err != nil {
					t.Fatal(err)
				}
			} else {
				f.Close()
			}
			info, err := state.Load()
			if info != nil || err == nil {
				t.Fatal("unreadable checkpoint returned usable progress")
			}
			if oversized {
				if !errors.Is(err, ErrResumeFileMismatch) {
					t.Fatalf("oversized checkpoint not rejected: %v", err)
				}
				stat, err := f.Stat()
				if err != nil || stat.Size() != maxCheckpointSize+1 {
					t.Fatal("oversized checkpoint changed", err)
				}
			} else {
				var pathErr *os.PathError
				if !errors.As(err, &pathErr) || errors.Is(err, ErrResumeFileMismatch) {
					t.Fatalf("I/O error misclassified as corrupt progress: %v", err)
				}
			}
		})
	}
}

func TestCheckpointLoadAllowsCompletedRangeMarkers(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		t.Run(fmt.Sprint(format), func(t *testing.T) {
			exported := &transfer.DownloadInstanceInfoExport{TotalSize: 8, GenBegin: 8, Ranges: transfer.RangeList{{Begin: 0, End: 8}, {Begin: 4, End: 4}}}
			var data []byte
			var err error
			if format == InstanceStateStorageFormatProto3 {
				data, err = proto.Marshal(exported)
			} else {
				data, err = json.Marshal(exported)
			}
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(t.TempDir(), "state")
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(name)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			state := NewInstanceState(f, format)
			defer state.Close()
			info, err := state.Load()
			if err != nil || info == nil || info.Ranges.Len() != 8 || info.DownloadStatus.Downloaded() != 0 {
				t.Fatalf("valid zero-length marker rejected: info=%v err=%v", info, err)
			}
		})
	}
}

func TestClosedCheckpointGetDoesNotPanic(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(f, InstanceStateStorageFormatJSON)
	defer state.Close()
	f.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("checkpoint read error panicked: %v", r)
		}
	}()
	if got := state.Get(); got != nil {
		t.Fatal("closed checkpoint returned progress")
	}
}
