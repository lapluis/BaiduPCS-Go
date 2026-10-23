package downloader

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestDownloaderNormalizesPrivateConfigCopy(t *testing.T) {
	for _, cacheSize := range []int{-16, 0, 1, 8192} {
		cfg := &Config{CacheSize: cacheSize, MaxParallel: 0}
		original := *cfg
		d := NewDownloader("", nil, cfg)
		d.lazyInit()
		if d.config.CacheSize < 1024 || d.config.MaxParallel < 1 {
			t.Errorf("configuration not normalized: %+v", d.config)
		}
		if *cfg != original {
			t.Error("normalization changed caller's shared configuration")
		}
		if cacheSize >= 1024 && d.config.CacheSize != cacheSize {
			t.Error("normalization changed a valid cache size")
		}
	}
}

func TestDownloadCompletesWithUninitializedCacheSettings(t *testing.T) {
	content := bytes.Repeat([]byte("cache fixture"), 2000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	for _, cacheSize := range []int{-16, 0, 1, 8192} {
		t.Run(strconv.Itoa(cacheSize), func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "data")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			cfg := &Config{CacheSize: cacheSize}
			d := NewDownloader(server.URL, f, cfg)
			done := make(chan error, 1)
			go func() { done <- d.Execute() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				d.Cancel()
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("download made no progress with uninitialized cache configuration")
			}
			got, err := os.ReadFile(f.Name())
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("download content mismatch: got %d bytes, %v", len(got), err)
			}
			if cfg.CacheSize != cacheSize || cfg.MaxParallel != 0 {
				t.Fatal("download modified caller configuration")
			}
		})
	}
}
