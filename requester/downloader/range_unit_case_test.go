package downloader

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultProbeKeepsMixedCaseByteRangeCapability(t *testing.T) {
	for _, units := range [][]string{{"bytes"}, {"Bytes"}, {"BYTES"}, {"items, BYTES"}, {"BYTES, items"}, {"items", "BYTES"}} {
		t.Run(fmt.Sprint(units), func(t *testing.T) {
			data := bytes.Repeat([]byte("range-unit-fixture"), 100000)
			var ranges atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				begin, end := 0, len(data)-1
				status := http.StatusOK
				if value := r.Header.Get("Range"); value != "" {
					if _, err := fmt.Sscanf(value, "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || end < begin || end >= len(data) {
						t.Errorf("invalid range %q", value)
						http.Error(w, "invalid range", http.StatusBadRequest)
						return
					}
					ranges.Add(1)
					status = http.StatusPartialContent
					w.Header().Set("Content-Range", fmt.Sprintf("bYtEs %d-%d/%d", begin, end, len(data)))
				}
				for _, unit := range units {
					w.Header().Add("Accept-Ranges", unit)
				}
				w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
				w.Header().Set("ETag", `"fixture"`)
				w.WriteHeader(status)
				w.Write(data[begin : end+1])
			}))
			defer server.Close()
			f, err := os.CreateTemp(t.TempDir(), "data")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 4, CacheSize: 8192})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := d.ExecuteWithContext(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(f.Name())
			if err != nil || !bytes.Equal(got, data) || ranges.Load() == 0 || d.firstInfo.AcceptRanges != "bytes" {
				t.Fatalf("range capability lost or content changed: ranges=%d capability=%q bytes=%d err=%v", ranges.Load(), d.firstInfo.AcceptRanges, len(got), err)
			}
		})
	}
}
