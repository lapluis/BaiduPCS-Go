package downloader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestUnsupportedProbeStatusPreservesOutput(t *testing.T) {
	for _, code := range []int{201, 202, 204, 205, 300, 301, 302, 303, 304, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			original := []byte("existing payload")
			if _, err := f.Write(original); err != nil {
				t.Fatal(err)
			}
			body := &unusedSingleResponse{Reader: bytes.NewReader(nil)}
			calls := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = queuedWriteTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)), ContentLength: 0, Header: http.Header{"Accept-Ranges": []string{"bytes"}}, Body: body, Request: r}, nil
			})
			d := NewDownloader("https://probe.fixture.invalid/file", f, &Config{MaxParallel: 1, InstanceStatePath: f.Name() + ".state"})
			d.SetClient(client)
			executed, succeeded := false, false
			d.OnExecute(func() { executed = true })
			d.OnSuccess(func() { succeeded = true })
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = d.ExecuteWithContext(ctx)
			data, readErr := os.ReadFile(f.Name())
			if err == nil || !strings.Contains(err.Error(), strconv.Itoa(code)) || ctx.Err() != nil || readErr != nil || !bytes.Equal(data, original) || calls != 1 || executed || succeeded || !body.closed || body.reads != 0 {
				t.Fatalf("unsupported probe accepted: status=%d err=%v data=%q read=%v calls=%d execute=%v success=%v closed=%v reads=%d", code, err, data, readErr, calls, executed, succeeded, body.closed, body.reads)
			}
		})
	}
}

func TestMirrorProbeRequiresFileResponseStatus(t *testing.T) {
	for _, code := range []int{200, 206, 201, 202, 204, 205, 300, 301, 302, 303, 304, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			d := NewDownloader("https://primary.fixture.invalid/file", nil, NewConfig())
			d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 8, AcceptRanges: "bytes"})
			d.AddLoadBalanceServer("https://mirror.fixture.invalid/file")
			body := &unusedSingleResponse{Reader: bytes.NewReader(nil)}
			d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
				r, err := http.NewRequest(http.MethodGet, url, nil)
				return 8, &http.Response{StatusCode: code, Status: fmt.Sprint(code), ContentLength: 8, Header: http.Header{"Accept-Ranges": []string{"bytes"}, "Content-Range": []string{"bytes 0-7/8"}}, Body: body, Request: r}, err
			})
			compares := 0
			d.SetLoadBalancerCompareFunc(func(_ map[string]string, _ *http.Response) bool { compares++; return true })
			d.lazyInit()
			results := d.checkLoadBalancers(context.Background())
			want := 1
			if code == 200 || code == 206 {
				want++
			}
			if len(results.lbr) != want || compares != want-1 || !body.closed || body.reads != 0 {
				t.Fatalf("mirror status=%d servers=%d want=%d comparisons=%d closed=%v reads=%d", code, len(results.lbr), want, compares, body.closed, body.reads)
			}
		})
	}
}

func TestHTTPProbeNoContentPreservesOutput(t *testing.T) {
	for _, code := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Accept-Ranges", "bytes")
				w.WriteHeader(code)
			}))
			defer server.Close()
			f, err := os.Create(filepath.Join(t.TempDir(), "payload"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("existing payload"); err != nil {
				t.Fatal(err)
			}
			d := NewDownloader(server.URL, f, NewConfig())
			err = d.Execute()
			data, readErr := os.ReadFile(f.Name())
			if err == nil || !strings.Contains(err.Error(), strconv.Itoa(code)) || readErr != nil || string(data) != "existing payload" {
				t.Fatalf("HTTP status %d changed output: err=%v data=%q read=%v", code, err, data, readErr)
			}
		})
	}
}

func TestRejectedProbeDecodesErrorBodyBeforeClosing(t *testing.T) {
	for _, code := range []int{400, 500} {
		for _, mirror := range []bool{false, true} {
			for _, decoderError := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/mirror=%v/error=%v", code, mirror, decoderError), func(t *testing.T) {
					d := NewDownloader("https://primary.fixture.invalid/file", nil, NewConfig())
					body := &unusedSingleResponse{Reader: strings.NewReader("details")}
					d.SetDURLCheckFunc(func(_ *requester.HTTPClient, url string) (int64, *http.Response, error) {
						r, err := http.NewRequest(http.MethodGet, url, nil)
						return 0, &http.Response{StatusCode: code, Header: make(http.Header), Body: body, Request: r}, err
					})
					cause := errors.New("decoded service error")
					decoded := 0
					d.SetStatusCodeBodyCheckFunc(func(r io.Reader) error {
						decoded++
						data, err := io.ReadAll(r)
						if err != nil || string(data) != "details" || body.closed {
							t.Error("decoder did not receive open error body", string(data), err, body.closed)
						}
						if decoderError {
							return cause
						}
						return nil
					})
					if mirror {
						d.SetFirstInfo(&DownloadFirstInfo{ContentLength: 0, AcceptRanges: "bytes"})
						d.AddLoadBalanceServer("https://mirror.fixture.invalid/file")
						d.lazyInit()
						if results := d.checkLoadBalancers(context.Background()); len(results.lbr) != 1 {
							t.Fatal("rejected mirror accepted")
						}
					} else {
						err := d.Execute()
						if err == nil || (decoderError && !errors.Is(err, cause)) || (!decoderError && !strings.Contains(err.Error(), strconv.Itoa(code))) {
							t.Fatal("probe error lost", err)
						}
					}
					if decoded != 1 || !body.closed {
						t.Fatal("decoder/close lifecycle", decoded, body.closed)
					}
				})
			}
		}
	}
}
