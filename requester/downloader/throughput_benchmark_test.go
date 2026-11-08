package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

// Measure the downloader, local HTTP and file Sync together. Integrity checking
// is outside the timer; no machine-dependent throughput threshold is asserted.
func BenchmarkDownloadToFile(b *testing.B) {
	const size = 64 << 20
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i*31 + i/4096)
	}
	wantHash := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "fixture.bin", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	for _, cache := range []int{8 << 10, 256 << 10} {
		for _, parallel := range []int{1, 4, 8} {
			b.Run(fmt.Sprintf("cache=%d/parallel=%d", cache, parallel), func(b *testing.B) {
				b.StopTimer()
				transport := &http.Transport{MaxIdleConnsPerHost: parallel}
				defer transport.CloseIdleConnections()
				client := requester.NewHTTPClient()
				// All requests go directly to the owned loopback server, regardless
				// of the user's proxy configuration or environment.
				client.Client.Transport = transport
				file, err := os.OpenFile(filepath.Join(b.TempDir(), "download.bin"), os.O_CREATE|os.O_RDWR, 0600)
				if err != nil {
					b.Fatal(err)
				}
				defer file.Close()
				b.SetBytes(size)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := file.Truncate(0); err != nil {
						b.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					d := NewDownloader(server.URL, file, &Config{MaxParallel: parallel, CacheSize: cache})
					d.SetClient(client)
					b.StartTimer()
					err := d.ExecuteWithContext(ctx)
					if err == nil {
						err = file.Sync()
					}
					b.StopTimer()
					cancel()
					if err != nil {
						b.Fatal(err)
					}
					info, err := file.Stat()
					if err != nil {
						b.Fatal(err)
					}
					if info.Size() != size {
						b.Fatalf("download size = %d, want %d", info.Size(), size)
					}
					if _, err := file.Seek(0, io.SeekStart); err != nil {
						b.Fatal(err)
					}
					hash := sha256.New()
					if _, err := io.Copy(hash, file); err != nil {
						b.Fatal(err)
					}
					if !bytes.Equal(hash.Sum(nil), wantHash[:]) {
						b.Fatal("downloaded content hash differs")
					}
					if got := d.monitor.Status().Downloaded(); got != size {
						b.Fatalf("reported bytes = %d, want %d", got, size)
					}
				}
			})
		}
	}
}
