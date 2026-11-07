package downloader

import (
	"bytes"
	"context"
	"fmt"
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

func TestDefaultPartialProbeReportsEntitySize(t *testing.T) {
	for _, tc := range []struct {
		bounds string
		want   int64
	}{{"bytes 0-3/8", 8}, {"bytes 0-3/*", -1}, {"bYtEs 0-3/8", 8}, {"BYTES 0-3/*", -1}} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%v", tc.bounds, contextual), func(t *testing.T) {
				body := &unusedSingleResponse{Reader: bytes.NewReader([]byte("abcd"))}
				defer body.Close()
				client := requester.NewHTTPClient()
				client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 206, ContentLength: 4, Header: http.Header{"Content-Range": []string{tc.bounds}}, Body: body, Request: r}, nil
				})
				probe := func() (int64, *http.Response, error) {
					return DefaultDURLCheckFunc(client, "https://probe.fixture.invalid/file")
				}
				if contextual {
					probe = func() (int64, *http.Response, error) {
						return defaultDURLCheckWithContext(context.Background(), client, "https://probe.fixture.invalid/file")
					}
				}
				size, resp, err := probe()
				if err != nil || size != tc.want || resp == nil || resp.ContentLength != 4 || body.closed || body.reads != 0 {
					t.Fatalf("partial body length used as entity size: size=%d want=%d err=%v resp=%v closed=%v reads=%d", size, tc.want, err, resp, body.closed, body.reads)
				}
			})
		}
	}
}

func TestDefaultPartialProbeDownloadsCompleteEntity(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			content := []byte("abcdefgh")
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") == "bytes=0-3" && unknown {
					w.Header().Set("Content-Range", "bytes 0-3/*")
					w.Header().Set("Content-Length", "4")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write(content[:4])
					return
				}
				wantRange := "bytes=0-3"
				if calls.Load() > 1 {
					wantRange = "bytes=4-7"
					if unknown {
						wantRange = ""
					}
				}
				if r.Header.Get("Range") != wantRange {
					t.Error("wrong probe/remainder range", r.Header.Get("Range"), wantRange)
				}
				http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
			}))
			defer server.Close()
			client := requester.NewHTTPClient()
			base := client.Client.Transport
			// A caller's transport can add a Range to the ordinary initial GET.
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					r = r.Clone(r.Context())
					r.Header.Set("Range", "bytes=0-3")
				}
				return base.RoundTrip(r)
			})
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			d := NewDownloader(server.URL, f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
			d.SetClient(client)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			err = d.ExecuteWithContext(ctx)
			data, readErr := os.ReadFile(f.Name())
			if err != nil || readErr != nil || !bytes.Equal(data, content) || calls.Load() != 2 {
				t.Fatalf("default partial probe failed: err=%v data=%q read=%v calls=%d", err, data, readErr, calls.Load())
			}
			if d.monitor.Status().Downloaded() != 8 || d.monitor.Status().TotalSize() != 8 {
				t.Fatal("wrong completed progress")
			}
		})
	}
}

func TestDefaultPartialProbeRejectsMalformedRange(t *testing.T) {
	for _, bounds := range []string{"", "items 0-3/8", "bytes 3-0/8", "bytes 0-3/3", "bytes 0-3/9223372036854775808"} {
		t.Run(bounds, func(t *testing.T) {
			body := &unusedSingleResponse{Reader: bytes.NewReader([]byte("abcd"))}
			defer body.Close()
			client := requester.NewHTTPClient()
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 206, ContentLength: 4, Header: http.Header{"Content-Range": []string{bounds}}, Body: body, Request: r}, nil
			})
			_, resp, err := DefaultDURLCheckFunc(client, "https://probe.fixture.invalid/file")
			// Returning the response keeps body ownership with the caller, also
			// on parse errors, just as other HTTP probe errors do.
			if err == nil || !strings.Contains(err.Error(), "partial") || resp == nil || body.closed || body.reads != 0 {
				t.Fatal("malformed range accepted or response ownership lost", err, resp, body.closed, body.reads)
			}
			if err := resp.Body.Close(); err != nil || !body.closed {
				t.Fatal("caller could not close returned response", err, body.closed)
			}
		})
	}
}

func TestDefaultPartialMirrorUsesEntitySize(t *testing.T) {
	content := []byte("abcdefgh")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-3" {
			t.Error("probe lost transport range")
		}
		http.ServeContent(w, r, "fixture", time.Unix(1, 0), bytes.NewReader(content))
	}))
	defer server.Close()
	client := requester.NewHTTPClient()
	base := client.Client.Transport
	client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Range", "bytes=0-3")
		return base.RoundTrip(r)
	})
	d := NewDownloader("https://primary.fixture.invalid/file", nil, NewConfig())
	d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"})
	d.SetClient(client)
	d.AddLoadBalanceServer(server.URL)
	d.lazyInit()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := d.checkLoadBalancers(ctx)
	if len(results.lbr) != 2 || results.lbr[1].URL != server.URL {
		t.Fatalf("healthy partial mirror discarded: %+v", results.lbr)
	}
}
