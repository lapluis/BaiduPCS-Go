package pcsdownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"github.com/qjfoidnh/BaiduPCS-Go/requester/downloader"
)

type probeBody struct {
	closed bool
	read   bool
}

func TestFailedProbeDoesNotInvalidateSavedEntity(t *testing.T) {
	for _, status := range []int{403, 429, 500} {
		for _, validator := range []string{"none", "etag", "modified"} {
			t.Run(fmt.Sprintf("%d/%s", status, validator), func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("ETag", `"error-document"`)
					w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error_code":31034,"error_msg":"temporary failure"}`)
				}))
				defer server.Close()
				name := filepath.Join(t.TempDir(), "partial")
				if err := os.WriteFile(name, []byte("partial"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(name+DownloadSuffix, []byte("saved-ranges"), 0600); err != nil {
					t.Fatal(err)
				}
				unit := emptyTask(name, true, false)
				unit.FileInfo.Size = 8
				if validator == "etag" {
					unit.ExpectedETag = `"file-entity"`
				}
				if validator == "modified" {
					unit.ExpectedModified = "Wed, 21 Oct 2015 07:27:00 GMT"
				}
				probed, reset := false, false
				unit.OnProbe = func(string, string) { probed = true }
				unit.OnTransferError = func(err error) { reset = errors.Is(err, downloader.ErrRemoteChanged) }
				err := unit.download(server.URL, requester.NewHTTPClient())
				if err == nil || errors.Is(err, downloader.ErrRemoteChanged) || reset || probed {
					t.Fatalf("error response treated as file entity: err=%v reset=%v probed=%v", err, reset, probed)
				}
				for path, want := range map[string]string{name: "partial", name + DownloadSuffix: "saved-ranges"} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Fatalf("saved data changed: %s %q %v", path, got, err)
					}
				}
			})
		}
	}
}

func TestSuccessfulProbeStillRejectsChangedEntity(t *testing.T) {
	for _, validator := range []string{"etag", "modified"} {
		t.Run(validator, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"replacement"`)
				w.Header().Set("Last-Modified", "Wed, 21 Oct 2015 07:28:00 GMT")
				_, _ = io.WriteString(w, "replaced")
			}))
			defer server.Close()
			unit := emptyTask(filepath.Join(t.TempDir(), "partial"), true, false)
			unit.FileInfo.Size = 8
			if validator == "etag" {
				unit.ExpectedETag = `"file-entity"`
			}
			if validator == "modified" {
				unit.ExpectedModified = "Wed, 21 Oct 2015 07:27:00 GMT"
			}
			reset, probed := false, false
			unit.OnTransferError = func(err error) { reset = errors.Is(err, downloader.ErrRemoteChanged) }
			unit.OnProbe = func(string, string) { probed = true }
			err := unit.download(server.URL, requester.NewHTTPClient())
			if !errors.Is(err, downloader.ErrRemoteChanged) || !reset || probed {
				t.Fatalf("changed entity accepted: err=%v reset=%v probed=%v", err, reset, probed)
			}
		})
	}
}

func (b *probeBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (b *probeBody) Close() error             { b.closed = true; return nil }

func TestEntityProbeDoesNotDownloadOrCreateTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		size   int64
		valid  bool
	}{
		{"current", 200, 8, true}, {"size-changed", 200, 9, false}, {"forbidden", 403, 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &probeBody{}
			client := requester.NewHTTPClient()
			client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
				if !strings.HasPrefix(req.Header.Get("Range"), "bytes=0-") {
					t.Error("missing bounded probe range")
				}
				return &http.Response{StatusCode: tc.status, ContentLength: tc.size, Header: http.Header{"Etag": []string{`"v1"`}}, Body: body, Request: req}, nil
			})
			target := filepath.Join(t.TempDir(), "never-created")
			seen := false
			unit := &DownloadTaskUnit{ProbeOnly: true, Context: context.Background(), SavePath: target, FileInfo: &baidupcs.FileDirectory{Size: 8}, OnProbe: func(etag, _ string) { seen = etag == `"v1"` }}
			err := unit.download("https://fixture.invalid/file", client)
			if (err == nil) != tc.valid || seen != tc.valid {
				t.Fatalf("err=%v seen=%v", err, seen)
			}
			if !body.closed || body.read {
				t.Fatalf("probe body closed=%v read=%v", body.closed, body.read)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatal("probe created destination")
			}
		})
	}
}
