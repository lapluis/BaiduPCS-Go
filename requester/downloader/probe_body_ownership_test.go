package downloader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type ownedProbeBody struct {
	io.Reader
	reads, closes atomic.Int32
	closeErr      error
}

func (b *ownedProbeBody) Read(p []byte) (int, error) {
	b.reads.Add(1)
	return b.Reader.Read(p)
}

func (b *ownedProbeBody) Close() error {
	if b.closes.Add(1) != 1 {
		return errors.New("probe response closed repeatedly")
	}
	return b.closeErr
}

func TestProbeBodyOwnershipAcrossExecutionPaths(t *testing.T) {
	for _, mode := range []string{"reused-full", "reused-full-close-error", "discarded-partial", "resumed", "rejected", "probe-error"} {
		t.Run(mode, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "data")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			original := "original-content"
			if mode == "resumed" {
				original = "abcd????"
			}
			if _, err := file.WriteString(original); err != nil {
				t.Fatal(err)
			}
			statePath := file.Name() + ".state"
			if mode == "resumed" {
				state := openOwnedCheckpoint(t, statePath, InstanceStateStorageFormatJSON)
				if err := state.Put(checkpointOwnerInfo(4)); err != nil {
					t.Fatal(err)
				}
				if err := state.Close(); err != nil {
					t.Fatal(err)
				}
			}
			probe := &ownedProbeBody{Reader: strings.NewReader("abcdefgh")}
			if mode == "reused-full-close-error" {
				probe.closeErr = errors.New("fixture first close failure")
			}
			client := requester.NewHTTPClient()
			requests := 0
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				status, data := http.StatusOK, "abcdefgh"
				header := make(http.Header)
				if mode == "resumed" {
					if r.Header.Get("Range") != "bytes=4-7" {
						t.Errorf("resume lost completed prefix: %q", r.Header.Get("Range"))
					}
					status, data = http.StatusPartialContent, "efgh"
					header.Set("Content-Range", "bytes 4-7/8")
				} else if r.Header.Get("Range") != "" {
					t.Errorf("full stream requested a range: %q", r.Header.Get("Range"))
				}
				return &http.Response{StatusCode: status, ContentLength: int64(len(data)), Header: header, Body: io.NopCloser(strings.NewReader(data)), Request: r}, nil
			})
			d := NewDownloader("https://probe.fixture.invalid/file", file, &Config{MaxParallel: 1, CacheSize: 1024, InstanceStatePath: statePath, InstanceStateStorageFormat: InstanceStateStorageFormatJSON})
			d.SetClient(client)
			cause := errors.New("fixture probe failure")
			var probeResponse *http.Response
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				req, err := http.NewRequest(http.MethodGet, url, nil)
				if err != nil {
					return 0, nil, err
				}
				resp := &http.Response{StatusCode: http.StatusOK, ContentLength: 8, Header: http.Header{"Accept-Ranges": {"bytes"}}, Body: probe, Request: req}
				probeResponse = resp
				switch mode {
				case "discarded-partial", "resumed":
					probe.Reader = strings.NewReader("abcd")
					resp.StatusCode, resp.ContentLength = http.StatusPartialContent, 4
					if mode == "discarded-partial" {
						resp.Header.Set("Content-Range", "bytes 0-3/*")
						return -1, resp, nil
					}
					resp.Header.Set("Content-Range", "bytes 0-3/8")
				case "rejected":
					resp.StatusCode, resp.ContentLength = http.StatusNoContent, 0
					return 0, resp, nil
				case "probe-error":
					return 8, resp, cause
				}
				return 8, resp, nil
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			runErr := d.ExecuteWithContext(ctx)
			want, wantRequests := "abcdefgh", 0
			if mode == "discarded-partial" || mode == "resumed" {
				wantRequests = 1
			}
			if mode == "rejected" || mode == "probe-error" {
				want = original
				if runErr == nil || mode == "probe-error" && !errors.Is(runErr, cause) {
					t.Errorf("probe failure lost: %v", runErr)
				}
			} else if runErr != nil {
				t.Fatal(runErr)
			}
			if closeErr := probeResponse.Body.Close(); closeErr != probe.closeErr {
				t.Errorf("shared cleanup lost the first Close result: got=%v want=%v", closeErr, probe.closeErr)
			}
			data, err := os.ReadFile(file.Name())
			if err != nil || string(data) != want || requests != wantRequests || probe.closes.Load() != 1 || !strings.HasPrefix(mode, "reused-full") && probe.reads.Load() != 0 {
				t.Fatalf("probe ownership or data changed: close=%d reads=%d requests=%d wantRequests=%d data=%q err=%v", probe.closes.Load(), probe.reads.Load(), requests, wantRequests, data, err)
			}
		})
	}
}
