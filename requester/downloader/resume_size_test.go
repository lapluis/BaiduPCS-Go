package downloader

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/transfer"
)

func TestResumeChecksRemoteSizeBeforeWriting(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, savedSize := range []int64{16, 32, 64} {
			name := "json"
			if format == InstanceStateStorageFormatProto3 {
				name = "protobuf"
			}
			name += "/" + map[int64]string{16: "remote_grew", 32: "same_size", 64: "remote_shrank"}[savedSize]
			t.Run(name, func(t *testing.T) {
				content := bytes.Repeat([]byte("a"), 32)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
				}))
				defer server.Close()
				dir := t.TempDir()
				filename, statePath := filepath.Join(dir, "data"), filepath.Join(dir, "state")
				partial := bytes.Repeat([]byte("a"), int(savedSize/2))
				if err := os.WriteFile(filename, partial, 0600); err != nil {
					t.Fatal(err)
				}
				exported := &transfer.DownloadInstanceInfoExport{TotalSize: savedSize, GenBegin: savedSize, Ranges: []*transfer.Range{{Begin: savedSize / 2, End: savedSize}}}
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
				if err := os.WriteFile(statePath, state, 0600); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(filename, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 8192, InstanceStatePath: statePath, InstanceStateStorageFormat: format})
				err = d.Execute()
				if d.instanceState != nil {
					d.instanceState.Close()
				}
				got, readErr := os.ReadFile(filename)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if savedSize == int64(len(content)) {
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(got, content) {
						t.Fatalf("matching resume content: got %d bytes", len(got))
					}
				} else {
					if !errors.Is(err, ErrResumeSizeMismatch) {
						t.Errorf("stale size %d for remote size %d: expected size mismatch, got %v", savedSize, len(content), err)
					}
					if !bytes.Equal(got, partial) {
						t.Error("mismatching resume modified partial data")
					}
					retained, readErr := os.ReadFile(statePath)
					if readErr != nil || !bytes.Equal(retained, state) {
						t.Errorf("mismatching resume failed to preserve state: %v", readErr)
					}
				}
			})
		}
	}
}
