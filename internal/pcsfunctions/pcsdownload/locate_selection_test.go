package pcsdownload

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestLocateDownloadSelectionBoundsAndRefresh(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	for _, tc := range []struct {
		name                  string
		prefer                int
		cached                bool
		wantFirst, wantSecond int
	}{
		{"negative", -1, false, 0, 0},
		{"minimum-int", -maxInt - 1, false, 0, 0},
		{"maximum-int", maxInt, false, 2, 1},
		{"beyond-list", 3, false, 2, 1},
		{"preferred", 1, false, 1, 1},
		{"cached-host-fallback", 0, true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if v := recover(); v != nil {
					t.Errorf("link preference %d caused panic: %v", tc.prefer, v)
				}
			}()
			panClientOnce.Do(func() { panClient = requester.NewHTTPClient() })
			previous := panClient
			t.Cleanup(func() { panClient = previous })
			var selected []string
			panClient = requester.NewHTTPClient()
			panClient.SetKeepAlive(false)
			panClient.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				selected = append(selected, req.URL.Host+req.URL.Path)
				return &http.Response{StatusCode: 200, ContentLength: 8, Header: http.Header{"X-Bs-File-Size": []string{"8"}}, Body: io.NopCloser(strings.NewReader("abcdefgh")), Request: req}, nil
			})
			lookups := 0
			client := requester.NewHTTPClient()
			client.SetKeepAlive(false)
			client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				lookups++
				if req.URL.Query().Get("method") != "locatedownload" {
					t.Errorf("unexpected discovery: %s", req.URL)
				}
				count := 3
				if lookups > 1 {
					count = 2
				}
				urls := make([]map[string]interface{}, count)
				for i := range urls {
					host := fmt.Sprintf("cdn-%d.fixture.invalid", i)
					if tc.cached && i == 0 {
						host = "nb.cache.fixture.invalid"
					}
					urls[i] = map[string]interface{}{"url": fmt.Sprintf("http://%s/round-%d", host, lookups), "encrypt": 0}
				}
				payload, err := json.Marshal(map[string]interface{}{"error_code": 0, "urls": urls})
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(payload))), Request: req}, nil
			})
			output := filepath.Join(t.TempDir(), "missing", "output")
			task := emptyTask(output, false, false)
			task.PCS = baidupcs.NewPCSWithClient(1, client)
			task.PCS.SetUID(1)
			task.FileInfo.Size = 8
			task.ProbeOnly = true
			task.DlinkPrefer = tc.prefer
			for round, want := range []int{tc.wantFirst, tc.wantSecond} {
				result := &taskframework.TaskUnitRunResult{}
				if !task.locateDownload(result) || result.Err != nil {
					t.Fatalf("selection failed: %+v", result)
				}
				expected := fmt.Sprintf("cdn-%d.fixture.invalid/round-%d", want, round+1)
				if lookups != round+1 || len(selected) != round+1 || selected[round] != expected {
					t.Fatalf("wrong link or stale discovery: lookups=%d selected=%v want=%s", lookups, selected, expected)
				}
			}
			if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
				t.Fatalf("link probe created output directory: %v", err)
			}
		})
	}
}

func TestLocateDownloadSkipsUnusableLinksBeforeProbe(t *testing.T) {
	for _, invalidOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(invalidOnly), func(t *testing.T) {
			installDownloadClient(t)
			probes := 0
			transport := panClient.Client.Transport
			panClient.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				probes++
				if req.URL.Host != "eligible.fixture.invalid" {
					t.Errorf("unusable or encrypted URL reached download probe: %s", req.URL)
				}
				return transport.RoundTrip(req)
			})
			lookups := 0
			client := requester.NewHTTPClient()
			client.SetKeepAlive(false)
			client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				lookups++
				payload := `{"error_code":0,"urls":[{"url":"","encrypt":0},{"url":"/relative","encrypt":0},{"url":"https://encrypted.fixture.invalid/file","encrypt":1},{"url":"ftp://unsupported.fixture.invalid/file","encrypt":0}`
				if !invalidOnly {
					payload += `,{"url":"https://eligible.fixture.invalid/file","encrypt":0}`
				}
				payload += `]}`
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			output := filepath.Join(t.TempDir(), "missing", "output")
			task := emptyTask(output, false, false)
			task.FileInfo.Size = 8
			task.ProbeOnly = true
			task.PCS = baidupcs.NewPCSWithClient(1, client)
			task.PCS.SetUID(1)
			result := &taskframework.TaskUnitRunResult{}
			ok := task.locateDownload(result)
			if invalidOnly {
				if ok || !errors.Is(result.Err, ErrDlinkNotFound) || probes != 0 {
					t.Fatalf("invalid list did not stop before download: ok=%v probes=%d result=%+v", ok, probes, result)
				}
			} else if !ok || result.Err != nil || probes != 1 {
				t.Fatalf("usable fallback was blocked: ok=%v probes=%d result=%+v", ok, probes, result)
			}
			if lookups != 1 {
				t.Fatalf("unexpected repeated discovery: %d", lookups)
			}
			if _, err := os.Stat(filepath.Dir(output)); !os.IsNotExist(err) {
				t.Fatalf("link probe created output directory: %v", err)
			}
		})
	}
}
