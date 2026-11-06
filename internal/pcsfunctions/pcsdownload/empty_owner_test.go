package pcsdownload

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

func TestEmptyDownloadCannotModifyActiveCheckpoint(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "data")
			if exists {
				if err := os.WriteFile(path, []byte("only copy"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			checkpoint := path + DownloadSuffix
			if err := os.WriteFile(checkpoint, []byte("active progress"), 0600); err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(checkpoint, os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			owner := downloader.NewInstanceState(file, downloader.InstanceStateStorageFormatJSON)
			defer owner.Close()
			result := emptyTask(path, true, false).Run()
			if result.Succeed || result.Err == nil {
				t.Errorf("empty download bypassed active owner: %+v", result)
			}
			data, err := os.ReadFile(path)
			if exists {
				if err != nil || string(data) != "only copy" {
					t.Errorf("active output changed: %q %v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Errorf("contender created output: %v", err)
			}
			if data, err := os.ReadFile(checkpoint); err != nil || string(data) != "active progress" {
				t.Errorf("active checkpoint changed: %q %v", data, err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			result = emptyTask(path, true, false).Run()
			if !result.Succeed || result.Err != nil {
				t.Fatalf("retry after release failed: %+v", result)
			}
			if data, err := os.ReadFile(path); err != nil || len(data) != 0 {
				t.Fatalf("empty download failed: %q %v", data, err)
			}
			if _, err := os.Stat(checkpoint); !os.IsNotExist(err) {
				t.Fatalf("empty download retained old ranges: %v", err)
			}
		})
	}
}

func TestEmptyProbeInspectsRemoteWithoutLocalWrites(t *testing.T) {
	panClientOnce.Do(func() { panClient = requester.NewHTTPClient() })
	previous := panClient
	t.Cleanup(func() { panClient = previous })
	probes := 0
	remoteStatus, remoteSize := 200, int64(0)
	panClient = requester.NewHTTPClient()
	panClient.Client.Transport = downloadRoundTrip(func(r *http.Request) (*http.Response, error) {
		probes++
		header := make(http.Header)
		header.Set("ETag", `"empty-v1"`)
		header.Set("X-Bs-File-Size", fmt.Sprint(remoteSize))
		return &http.Response{StatusCode: remoteStatus, ContentLength: remoteSize, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	client := requester.NewHTTPClient()
	client.Client.Transport = downloadRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			r.Body.Close()
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error_code":0,"urls":[{"url":"https://empty.fixture.invalid/file","encrypt":0}]}`)), Request: r}, nil
	})
	for _, remote := range []struct {
		status int
		size   int64
	}{{200, 0}, {200, 8}, {403, 0}} {
		remoteStatus, remoteSize = remote.status, remote.size
		valid := remoteStatus == 200 && remoteSize == 0
		for _, exists := range []bool{false, true} {
			for _, overwrite := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%d/%t/%t", remoteStatus, remoteSize, exists, overwrite), func(t *testing.T) {
					root := t.TempDir()
					path := filepath.Join(root, "missing", "data")
					if exists {
						path = filepath.Join(root, "data")
						if err := os.WriteFile(path, []byte("keep local"), 0600); err != nil {
							t.Fatal(err)
						}
						checkpoint, err := os.OpenFile(path+DownloadSuffix, os.O_CREATE|os.O_RDWR, 0600)
						if err != nil {
							t.Fatal(err)
						}
						if _, err := checkpoint.WriteString("active progress"); err != nil {
							checkpoint.Close()
							t.Fatal(err)
						}
						owner := downloader.NewInstanceState(checkpoint, downloader.InstanceStateStorageFormatJSON)
						defer owner.Close()
					}
					unit := emptyTask(path, overwrite, false)
					unit.ProbeOnly = true
					unit.PCS = baidupcs.NewPCSWithClient(1, client)
					unit.PCS.SetUID(1)
					seen := false
					unit.OnProbe = func(etag, _ string) { seen = etag == `"empty-v1"` }
					before := probes
					result := unit.Run()
					if result.Succeed != valid || (result.Err == nil) != valid || seen != valid || probes != before+1 {
						t.Errorf("empty probe skipped remote inspection: %+v seen=%t probes=%d", result, seen, probes-before)
					}
					if exists {
						if got, err := os.ReadFile(path); err != nil || string(got) != "keep local" {
							t.Errorf("probe changed data: %q %v", got, err)
						}
						if got, err := os.ReadFile(path + DownloadSuffix); err != nil || string(got) != "active progress" {
							t.Errorf("probe changed checkpoint: %q %v", got, err)
						}
					} else if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
						t.Errorf("probe created output directory: %v", err)
					}
				})
			}
		}
	}
}
