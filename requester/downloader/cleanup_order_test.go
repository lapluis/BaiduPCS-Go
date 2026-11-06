package downloader

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestDownloadSuccessFollowsCheckpointCleanup(t *testing.T) {
	content := []byte("complete")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	statePath := f.Name() + ".state"
	d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath})
	d.SetFileContentLength(8)
	var succeeded, finished atomic.Int64
	d.OnSuccess(func() {
		succeeded.Add(1)
		if _, err := os.Stat(statePath); !os.IsNotExist(err) {
			t.Error("success callback ran before checkpoint cleanup")
		}
	})
	d.OnFinish(func() {
		if succeeded.Load() != 1 {
			t.Error("finish callback ran before success callback")
		}
		finished.Add(1)
	})
	if err := d.Execute(); err != nil {
		t.Fatal(err)
	}
	if succeeded.Load() != 1 || finished.Load() != 1 {
		t.Fatal("download lifecycle callbacks lost")
	}
}
