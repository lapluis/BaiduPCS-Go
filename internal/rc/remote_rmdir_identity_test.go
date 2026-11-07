package rc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestRemoteRmdirRechecksIdentityAfterListing(t *testing.T) {
	for _, mode := range []string{"unchanged", "replaced", "file", "missing", "metadata-error", "canceled", "nonempty"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			metadata, listings, deletions := 0, 0, 0
			cause := errors.New("fixture final metadata failure")
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"error_code":0}`
				if req.URL.Path == "/api/list" {
					listings++
					payload = `{"errno":0,"list":[]}`
					if mode == "nonempty" {
						payload = `{"errno":0,"list":[{"fs_id":9,"path":"/fixture/child","size":1}]}`
					}
				} else if req.URL.Query().Get("method") == "meta" {
					metadata++
					id, isdir := 7, 1
					if listings > 0 {
						switch mode {
						case "replaced":
							id = 8
						case "file":
							isdir = 0
						case "missing":
							return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error_code":31066}`)), Request: req}, nil
						case "metadata-error":
							return nil, cause
						case "canceled":
							cancel()
						}
					}
					payload = fmt.Sprintf(`{"error_code":0,"list":[{"fs_id":%d,"path":"/fixture","isdir":%d}]}`, id, isdir)
				} else {
					deletions++
					if req.URL.Query().Get("method") != "delete" {
						t.Error("unexpected request")
					}
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
			err := remote.Remove(ctx, "/fixture", true)
			if mode == "unchanged" {
				if err != nil || metadata != 2 || listings != 1 || deletions != 1 {
					t.Fatalf("stable directory removal failed: meta=%d list=%d delete=%d err=%v", metadata, listings, deletions, err)
				}
			} else if err == nil || listings != 1 || deletions != 0 {
				t.Errorf("changed/unverified directory deleted: meta=%d list=%d delete=%d err=%v", metadata, listings, deletions, err)
			}
			if mode == "metadata-error" && !errors.Is(err, cause) {
				t.Errorf("final metadata cause lost: %v", err)
			}
			if mode == "canceled" && !errors.Is(err, context.Canceled) {
				t.Errorf("final metadata cancellation lost: %v", err)
			}
			if mode == "nonempty" && metadata != 1 {
				t.Errorf("nonempty directory performed unnecessary metadata checks: %d", metadata)
			}
		})
	}
}
