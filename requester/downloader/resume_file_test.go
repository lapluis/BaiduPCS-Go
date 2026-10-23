package downloader

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestResumeRequiresCompletedBytesInLocalFile(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, localSize := range []int{0, 7, 8, 12, 24, 33} {
			t.Run(strconv.Itoa(int(format))+"/"+strconv.Itoa(localSize), func(t *testing.T) {
				content := []byte("0123456789abcdefghijklmnopqrstuv")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
				}))
				defer server.Close()
				dir := t.TempDir()
				filename, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
				partial := append([]byte(nil), append(content, 'x')[:localSize]...)
				if localSize != 0 {
					if err := os.WriteFile(filename, partial, 0600); err != nil {
						t.Fatal(err)
					}
				}
				f, err := os.OpenFile(filename, os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				exported := &transfer.DownloadInstanceInfoExport{TotalSize: 32, GenBegin: 32, Ranges: []*transfer.Range{{Begin: 8, End: 32}}}
				var state []byte
				if format == InstanceStateStorageFormatProto3 {
					state, err = proto.Marshal(exported)
				} else {
					state, err = json.Marshal(exported)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statePath, state, 0600); err != nil {
					t.Fatal(err)
				}
				d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath, InstanceStateStorageFormat: format})
				err = d.Execute()
				if d.instanceState != nil {
					d.instanceState.Close()
				}
				got, readErr := os.ReadFile(filename)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if localSize >= 8 && localSize <= 32 {
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, content) {
						t.Fatalf("valid partial file produced incorrect data: %q", got)
					}
				} else {
					if !errors.Is(err, ErrResumeFileMismatch) {
						t.Errorf("expected local resume mismatch, got %v", err)
					}
					if !bytes.Equal(got, partial) {
						t.Error("invalid resume changed partial file")
					}
					retained, readErr := os.ReadFile(statePath)
					if readErr != nil || !bytes.Equal(retained, state) {
						t.Errorf("invalid resume did not retain state: %v", readErr)
					}
				}
			})
		}
	}
}

func TestResumeFileExtentWithParallelAndBlockRanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		localSize int64
		generated int64
		block     bool
		ranges    transfer.RangeList
		valid     bool
	}{
		{"parallel_pending_suffix", 8, 32, false, transfer.RangeList{{Begin: 16, End: 32}, {Begin: 8, End: 16}}, true},
		{"parallel_missing_completed_middle", 8, 32, false, transfer.RangeList{{Begin: 24, End: 32}, {Begin: 8, End: 16}}, false},
		{"parallel_completed_middle_present", 24, 32, false, transfer.RangeList{{Begin: 24, End: 32}, {Begin: 8, End: 16}}, true},
		{"block_future_unassigned", 8, 16, true, transfer.RangeList{{Begin: 8, End: 16}}, true},
		{"block_missing_completed_prefix", 7, 16, true, transfer.RangeList{{Begin: 8, End: 16}}, false},
		{"block_no_completed_data", 0, 16, true, transfer.RangeList{{Begin: 0, End: 16}}, true},
		{"completed_file_present", 32, 32, false, nil, true},
		{"completed_file_truncated", 31, 32, false, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "data")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := f.Truncate(tc.localSize); err != nil {
				t.Fatal(err)
			}
			mode := transfer.RangeGenMode_Default
			if tc.block {
				mode = transfer.RangeGenMode_BlockSize
			}
			saved := &transfer.DownloadInstanceInfoExport{TotalSize: 32, GenBegin: tc.generated, BlockSize: 8, RangeGenMode: mode, Ranges: tc.ranges}
			d := NewDownloader("", f, NewConfig())
			err = d.validateResumeFile(saved.GetInstanceInfo())
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, got %v", tc.valid, err)
			}
		})
	}
}
