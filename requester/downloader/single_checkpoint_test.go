package downloader

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSingleDownloadRejectsActiveCheckpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "abcdefgh")
	}))
	defer server.Close()
	root := t.TempDir()
	path := filepath.Join(root, "state")
	state := openOwnedCheckpoint(t, path, InstanceStateStorageFormatJSON)
	if err := state.Put(checkpointOwnerInfo(4)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "data")
	if err := os.WriteFile(name, []byte("keepdata"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	d := NewDownloader(server.URL, file, &Config{InstanceStatePath: path})
	if err := d.Execute(); err == nil || errors.Is(err, ErrResumeFileMismatch) {
		t.Errorf("single stream did not reject active checkpoint: %v", err)
	}
	if got, err := os.ReadFile(name); err != nil || string(got) != "keepdata" {
		t.Errorf("contender changed active data: %q %v", got, err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, before) {
		t.Errorf("contender changed active checkpoint: %q %v", got, err)
	}
}

func TestSingleDownloadInvalidatesOldRangesBeforeWriting(t *testing.T) {
	for _, format := range []InstanceStateStorageFormat{InstanceStateStorageFormatJSON, InstanceStateStorageFormatProto3} {
		for _, complete := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", format, complete), func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, "state")
				state := openOwnedCheckpoint(t, path, format)
				if err := state.Put(checkpointOwnerInfo(4)); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(root, "data")
				if err := os.WriteFile(name, []byte("OLD!tail"), 0600); err != nil {
					t.Fatal(err)
				}
				file, err := os.OpenFile(name, os.O_RDWR, 0600)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", "8")
					if complete {
						_, _ = io.WriteString(w, "abcdefgh")
					} else {
						_, _ = io.WriteString(w, "xxxx")
					}
				}))
				defer server.Close()
				d := NewDownloader(server.URL, file, &Config{InstanceStatePath: path, InstanceStateStorageFormat: format})
				err = d.Execute()
				if complete {
					if err != nil {
						t.Fatal(err)
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("successful single download retained stale ranges: %v", err)
					}
				} else {
					if !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("expected interrupted stream: %v", err)
					}
					// A later range-capable attempt must fetch the entire representation,
					// including the prefix overwritten by the failed single stream.
					ranged := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader([]byte("abcdefgh")))
					}))
					defer ranged.Close()
					retry := NewDownloader(ranged.URL, file, &Config{InstanceStatePath: path, InstanceStateStorageFormat: format})
					if err := retry.Execute(); err != nil {
						t.Fatal(err)
					}
				}
				if got, err := os.ReadFile(name); err != nil || string(got) != "abcdefgh" {
					t.Fatalf("stale ranges corrupted retry: %q %v", got, err)
				}
			})
		}
	}
}

func TestSingleDownloadDiscardFailurePreservesData(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state")
	if err := os.WriteFile(path, []byte("retain checkpoint"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path) // Read-only handle cannot discard old ranges.
	if err != nil {
		t.Fatal(err)
	}
	state := NewInstanceState(file, InstanceStateStorageFormatJSON)
	defer state.Close()
	if err := state.discardRanges(); !errors.Is(err, ErrCheckpointSave) {
		t.Fatalf("discard error classification lost: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "retain checkpoint" {
		t.Fatalf("discard failure destroyed checkpoint: %q %v", got, err)
	}
}
