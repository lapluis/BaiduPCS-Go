package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestInvalidPartialProbeCannotPlanOrTruncate(t *testing.T) {
	for _, tc := range []struct {
		name, bounds  string
		total, length int64
	}{
		{"empty-missing-range", "", 0, 0},
		{"empty-contradictory-range", "bytes 0-0/8", 0, 0},
		{"empty-unknown-total", "bytes 0-0/*", 0, 0},
		{"missing-range", "", 8, 4},
		{"wrong-unit", "items 0-3/8", 8, 4},
		{"reversed-range", "bytes 3-0/8", 8, 4},
		{"wrong-total", "bytes 0-3/9", 8, 4},
		{"unknown-total", "bytes 0-3/*", 8, 4},
		{"wrong-body-length", "bytes 0-3/8", 8, 3},
		{"overflow", "bytes 0-9223372036854775807/*", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("existing"); err != nil {
				t.Fatal(err)
			}
			body := &unusedSingleResponse{Reader: bytes.NewReader([]byte("abcd"))}
			d := NewDownloader("https://probe.fixture.invalid/file", f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				r, err := http.NewRequest(http.MethodGet, url, nil)
				return tc.total, &http.Response{StatusCode: 206, ContentLength: tc.length, Header: http.Header{"Content-Range": []string{tc.bounds}}, Body: body, Request: r}, err
			})
			executed, succeeded := false, false
			d.OnExecute(func() { executed = true })
			d.OnSuccess(func() { succeeded = true })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = d.ExecuteWithContext(ctx)
			data, readErr := os.ReadFile(f.Name())
			if err == nil || !strings.Contains(err.Error(), "partial") || ctx.Err() != nil || readErr != nil || string(data) != "existing" || executed || succeeded || !body.closed || body.reads != 0 {
				t.Fatalf("invalid probe changed execution: err=%v data=%q read=%v executed=%v success=%v closed=%v reads=%d", err, data, readErr, executed, succeeded, body.closed, body.reads)
			}
			if _, err := os.Stat(f.Name() + ".state"); !os.IsNotExist(err) {
				t.Fatal("invalid probe created checkpoint", err)
			}
		})
	}
}

func TestHTTPEmptyPartialProbePreservesOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-0/8")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusPartialContent)
	}))
	defer server.Close()
	f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("existing"); err != nil {
		t.Fatal(err)
	}
	err = NewDownloader(server.URL, f, NewConfig()).Execute()
	data, readErr := os.ReadFile(f.Name())
	if err == nil || !strings.Contains(err.Error(), "partial") || readErr != nil || string(data) != "existing" {
		t.Fatal("empty partial response truncated output", err, string(data), readErr)
	}
}

func TestPartialMirrorProbeChecksMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, bounds string
		length       int64
		valid        bool
	}{
		{"prefix", "bytes 0-3/8", 4, true},
		{"suffix", "bytes 4-7/8", 4, true},
		{"unknown-body-length", "bytes 0-3/8", -1, true},
		{"missing", "", 4, false},
		{"wrong-length", "bytes 0-3/8", 3, false},
		{"zero-length", "bytes 0-3/8", 0, false},
		{"wrong-total", "bytes 0-3/9", 4, false},
		{"unknown-total", "bytes 0-3/*", 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDownloader("https://primary.fixture.invalid/file", nil, NewConfig())
			d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"})
			d.AddLoadBalanceServer("https://mirror.fixture.invalid/file")
			body := &unusedSingleResponse{Reader: bytes.NewReader(nil)}
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				r, err := http.NewRequest(http.MethodGet, url, nil)
				return 8, &http.Response{StatusCode: 206, ContentLength: tc.length, Header: http.Header{"Accept-Ranges": []string{"bytes"}, "Content-Range": []string{tc.bounds}}, Body: body, Request: r}, err
			})
			d.lazyInit()
			results := d.checkLoadBalancers(context.Background())
			want := 1
			if tc.valid {
				want++
			}
			if len(results.lbr) != want || !body.closed || body.reads != 0 {
				t.Fatal("mirror metadata classification", len(results.lbr), want, body.closed, body.reads)
			}
		})
	}
}

func TestInvalidPartialProbePreservesCheckpoint(t *testing.T) {
	for _, total := range []int64{8, -1} {
		t.Run(fmt.Sprint(total), func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("existing"); err != nil {
				t.Fatal(err)
			}
			statePath := f.Name() + ".state"
			state := openOwnedCheckpoint(t, statePath, InstanceStateStorageFormatJSON)
			if err := state.Put(checkpointOwnerInfo(4)); err != nil {
				t.Fatal(err)
			}
			if err := state.Close(); err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			body := &unusedSingleResponse{Reader: bytes.NewReader(nil)}
			d := NewDownloader("https://probe.fixture.invalid/file", f, &Config{MaxParallel: 1, InstanceStatePath: statePath})
			client := requester.NewHTTPClient()
			calls := 0
			client.Client.Transport = queuedWriteTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected request") })
			d.SetClient(client)
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				r, err := http.NewRequest(http.MethodGet, url, nil)
				return total, &http.Response{StatusCode: 206, ContentLength: 0, Header: make(http.Header), Body: body, Request: r}, err
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = d.ExecuteWithContext(ctx)
			saved, readErr := os.ReadFile(statePath)
			data, dataErr := os.ReadFile(f.Name())
			if err == nil || !strings.Contains(err.Error(), "partial") || calls != 0 || readErr != nil || !bytes.Equal(saved, original) || dataErr != nil || string(data) != "existing" || !body.closed || body.reads != 0 {
				t.Fatalf("invalid probe changed resume data: err=%v calls=%d saved=%q read=%v data=%q read=%v closed=%v reads=%d", err, calls, saved, readErr, data, dataErr, body.closed, body.reads)
			}
		})
	}
}
