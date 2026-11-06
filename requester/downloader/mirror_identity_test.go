package downloader

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestMirrorProbeChecksPrimaryEntityValidator(t *testing.T) {
	for _, tc := range []struct {
		name, primaryETag, mirrorETag, primaryModified, mirrorModified string
		valid                                                          bool
	}{
		{"same-etag", `"v1"`, `"v1"`, "old-date", "different-date", true},
		{"changed-etag", `"v1"`, `"v2"`, "same-date", "same-date", false},
		{"missing-etag", `"v1"`, "", "same-date", "same-date", false},
		{"same-modified", "", "", "same-date", "same-date", true},
		{"changed-modified", "", "", "old-date", "new-date", false},
		{"missing-modified", "", "", "old-date", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDownloader("https://primary.invalid/file", nil, NewConfig())
			d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes", ETag: tc.primaryETag, LastModified: tc.primaryModified})
			d.AddLoadBalanceServer("https://mirror.invalid/file")
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				req, err := http.NewRequest(http.MethodGet, url, nil)
				if err != nil {
					return 0, nil, err
				}
				return 8, &http.Response{StatusCode: 200, Header: http.Header{"Accept-Ranges": []string{"bytes"}, "Etag": []string{tc.mirrorETag}, "Last-Modified": []string{tc.mirrorModified}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
			})
			d.lazyInit()
			results := d.checkLoadBalancers(context.Background())
			want := 1
			if tc.valid {
				want++
			}
			if len(results.lbr) != want {
				t.Fatalf("mirror identity classification incorrect: servers=%d want=%d", len(results.lbr), want)
			}
		})
	}
}

func TestDownloadUsesHealthyMirrorsAndExcludesOtherEntities(t *testing.T) {
	content := bytes.Repeat([]byte("source-version"), 160000)
	wrong := bytes.Repeat([]byte("x"), len(content))
	var badRanges, goodRanges atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, tag, modified := content, `"v1"`, time.Unix(1, 0)
		if r.URL.Path == "/bad" {
			data, tag = wrong, `"v2"`
			if r.Header.Get("Range") != "" {
				badRanges.Add(1)
			}
		}
		if r.URL.Path == "/good" {
			modified = time.Unix(2, 0) // ETag is authoritative across mirror clocks.
			if r.Header.Get("Range") != "" {
				goodRanges.Add(1)
			}
		}
		w.Header().Set("ETag", tag)
		http.ServeContent(w, r, "fixture", modified, bytes.NewReader(data))
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := NewDownloader(server.URL+"/main", f, &Config{MaxParallel: 4, CacheSize: 8192, InstanceStatePath: f.Name() + ".state"})
	d.AddLoadBalanceServer(server.URL+"/bad", server.URL+"/good")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.ExecuteWithContext(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || !bytes.Equal(got, content) {
		t.Fatal("mirror transfer changed file contents", err)
	}
	if badRanges.Load() != 0 || goodRanges.Load() == 0 {
		t.Fatalf("wrong mirrors used: bad=%d good=%d", badRanges.Load(), goodRanges.Load())
	}
}

func TestResponseFirstInfoKeepsEntityMetadata(t *testing.T) {
	resp := &http.Response{Header: http.Header{"Etag": []string{`"v1"`}, "Last-Modified": []string{"date"}, "Content-Md5": []string{"md5"}, "X-Bs-Meta-Crc32": []string{"crc"}}}
	info := NewDownloadFirstInfoByResp(8, resp)
	if info.ETag != `"v1"` || info.LastModified != "date" || info.ContentMD5 != "md5" || info.ContentCRC32 != "crc" {
		t.Fatalf("entity metadata discarded: %+v", info)
	}
}

func TestFirstInfoCompareRejectsChangedEntity(t *testing.T) {
	first := &DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes", ETag: `"v1"`, LastModified: "date"}
	other := *first
	other.ETag = `"v2"`
	if first.Compare(&other) {
		t.Fatal("changed ETag compared equal")
	}
	first.ETag, other.ETag = "", ""
	other.LastModified = "new-date"
	if first.Compare(&other) {
		t.Fatal("changed Last-Modified compared equal")
	}
	first.ETag, other.ETag = `"v1"`, `"v1"`
	if !first.Compare(&other) {
		t.Fatal("matching ETag must take priority over Last-Modified")
	}
}
