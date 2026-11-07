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

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestMirrorPartialResponseProvesRangeCapability(t *testing.T) {
	for _, probe := range []struct {
		code      int
		requested bool
	}{{200, false}, {200, true}, {206, false}, {206, true}} {
		code := probe.code
		for _, advertised := range []string{"", "none", "bytes", "Bytes", "BYTES", "byteſ", "items, BYTES", "BYTES, items"} {
			for _, matchingEntity := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%s/requested=%t/entity=%t", code, advertised, probe.requested, matchingEntity), func(t *testing.T) {
					d := NewDownloader("https://primary.invalid/file", nil, NewConfig())
					d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes", ETag: `"v1"`})
					d.AddLoadBalanceServer("https://mirror.invalid/file")
					body := &unusedSingleResponse{Reader: bytes.NewReader(nil)}
					d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
						req, err := http.NewRequest(http.MethodGet, url, nil)
						if probe.requested {
							req.Header.Set("Range", "bytes=0-7")
						}
						tag := `"v1"`
						if !matchingEntity {
							tag = `"v2"`
						}
						return 8, &http.Response{StatusCode: code, ContentLength: 8, Header: http.Header{
							"Accept-Ranges": {advertised}, "Content-Range": {"bytes 0-7/8"}, "Etag": {tag},
						}, Body: body, Request: req}, err
					})
					d.lazyInit()
					results := d.checkLoadBalancers(context.Background())
					want := 1
					byteUnit := advertised == "bytes" || advertised == "Bytes" || advertised == "BYTES" || advertised == "items, BYTES" || advertised == "BYTES, items"
					if matchingEntity && (code == 206 || (byteUnit && !probe.requested)) {
						want++
					}
					if len(results.lbr) != want || !body.closed || body.reads != 0 {
						t.Fatalf("incorrect mirror capability: servers=%d want=%d closed=%t reads=%d", len(results.lbr), want, body.closed, body.reads)
					}
				})
			}
		}
	}
}

func TestDownloadUsesPartialMirrorWithoutRangeAdvertisement(t *testing.T) {
	data := bytes.Repeat([]byte("mirror-fixture"), 150000)
	var mirrorTransfers atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var begin, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &begin, &end); err != nil || begin < 0 || end < begin || end >= len(data) {
			t.Errorf("invalid range %q", r.Header.Get("Range"))
			http.Error(w, "invalid range", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/mirror" && r.Header.Get("Range") != "bytes=0-3" {
			mirrorTransfers.Add(1)
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Length", fmt.Sprint(end-begin+1))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", begin, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(data[begin : end+1])
	}))
	defer server.Close()
	f, err := os.CreateTemp(t.TempDir(), "data")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL+"/primary", f, &Config{MaxParallel: 4, CacheSize: 8192})
	d.AddLoadBalanceServer(server.URL + "/mirror")
	d.SetDURLCheckFunc(func(client *requester.HTTPClient, url string) (int64, *http.Response, error) {
		resp, err := client.Req(http.MethodGet, url, nil, map[string]string{"Range": "bytes=0-3"})
		return int64(len(data)), resp, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, data) || mirrorTransfers.Load() == 0 {
		t.Fatalf("healthy partial mirror not used or content changed: mirror=%d bytes=%d err=%v", mirrorTransfers.Load(), len(got), err)
	}
}
