package rc

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestInvalidRemoteDirectoryFlagBlocksPlan(t *testing.T) {
	for _, mode := range []string{"copy", "sync", "move"} {
		t.Run(mode, func(t *testing.T) {
			_, dst := endpoints(t)
			write(t, dst, "only-copy", "retained content")
			calls := 0
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Path != "/api/list" {
					t.Error("unexpected request", req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"errno":0,"list":[{"fs_id":1,"path":"/one","size":0,"isdir":2}]}`)), Request: req}, nil
			})
			src := Endpoint{FS: &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}, Root: "/"}
			plan, err := BuildPlan(context.Background(), src, dst, mode, opts())
			if err == nil || plan != nil || calls != 1 {
				t.Fatalf("invalid directory flag authorized plan: plan=%t calls=%d err=%v", plan != nil, calls, err)
			}
			if data, err := os.ReadFile(dst.At("only-copy")); err != nil || string(data) != "retained content" {
				t.Fatalf("destination changed: %q %v", data, err)
			}
		})
	}
}
