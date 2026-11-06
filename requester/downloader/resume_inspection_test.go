package downloader

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type resumeInspectionWriter struct {
	*os.File
	failure error
	writes  int
}

func (w *resumeInspectionWriter) Stat() (os.FileInfo, error) {
	if w.failure != nil {
		return nil, w.failure
	}
	return w.File.Stat()
}
func (w *resumeInspectionWriter) WriteAt(p []byte, off int64) (int, error) {
	w.writes++
	return w.File.WriteAt(p, off)
}

func TestResumeInspectionFailureRetainsProgressAndCause(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, cause := range []error{os.ErrPermission, syscall.EIO, os.ErrClosed} {
			t.Run(fmt.Sprintf("%d/%v", format, cause), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "state")
				state := openOwnedCheckpoint(t, path, format)
				if err := state.Put(checkpointOwnerInfo(4)); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				checkpoint, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(root, "data")
				original := []byte("abcd????")
				if err := os.WriteFile(name, original, 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(name, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				failure := &os.PathError{Op: "stat", Path: name, Err: cause}
				writer := &resumeInspectionWriter{File: file, failure: failure}
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Header.Get("Range") != "bytes=4-7" {
						t.Errorf("resume did not reuse saved prefix: %s", r.Header.Get("Range"))
					}
					http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
				}))
				defer server.Close()
				config := &Config{InstanceStatePath: path, InstanceStateStorageFormat: format, MaxParallel: 1}
				d := NewDownloader(server.URL, writer, config)
				d.SetFileContentLength(8)
				started, succeeded := false, false
				d.OnExecute(func() { started = true })
				d.OnSuccess(func() { succeeded = true })
				err = d.Execute()
				var pathErr *os.PathError
				if !errors.Is(err, cause) || !errors.As(err, &pathErr) || pathErr != failure || errors.Is(err, ErrResumeFileMismatch) || errors.Is(err, ErrCheckpointSave) {
					t.Errorf("inspection failure lost cause or requested destructive reset: %v", err)
				}
				if requests.Load() != 0 || writer.writes != 0 || started || succeeded {
					t.Errorf("failed inspection started a transfer: requests=%d writes=%d started=%t success=%t", requests.Load(), writer.writes, started, succeeded)
				}
				if got, err := os.ReadFile(name); err != nil || !bytes.Equal(got, original) {
					t.Fatalf("failed inspection changed payload: %q %v", got, err)
				}
				if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, checkpoint) {
					t.Fatalf("failed inspection changed checkpoint: %q %v", got, err)
				}
				writer.failure = nil
				retry := NewDownloader(server.URL, writer, config)
				retry.SetFileContentLength(8)
				if err := retry.Execute(); err != nil {
					t.Fatal("retry did not recover:", err)
				}
				if got, err := os.ReadFile(name); err != nil || string(got) != "abcdefgh" {
					t.Fatalf("retry did not preserve completed prefix: %q %v", got, err)
				}
				if requests.Load() != 1 {
					t.Errorf("retry did not fetch only unfinished range: %d", requests.Load())
				}
			})
		}
	}
}
