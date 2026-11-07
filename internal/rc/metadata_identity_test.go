package rc

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestInvalidRemoteIdentityCannotAuthorizeOperations(t *testing.T) {
	for _, identity := range []string{"", `,"fs_id":0`, `,"fs_id":-1`} {
		for _, operation := range []string{"stat", "worker", "mkdir", "remove", "fileplan"} {
			t.Run(operation+identity, func(t *testing.T) {
				calls, mutations := 0, 0
				client := requester.NewHTTPClient()
				client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Body != nil {
						defer req.Body.Close()
					}
					if req.URL.Query().Get("method") != "meta" {
						mutations++
					}
					payload := fmt.Sprintf(`{"error_code":0,"list":[{"path":"/fixture","size":8%s}]}`, identity)
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
				src, _ := endpoints(t)
				write(t, src, "source", "original")
				var err error
				switch operation {
				case "stat":
					_, err = remote.Stat(context.Background(), "/fixture")
				case "worker":
					_, err = workerDownloadInfo(context.Background(), remote.PCS, "/fixture")
				case "mkdir":
					err = remote.Mkdir(context.Background(), "/fixture")
				case "remove":
					err = remote.Remove(context.Background(), "/fixture", false)
				case "fileplan":
					o := opts()
					o.IgnoreExisting = true
					_, err = FilePlan(context.Background(), Endpoint{FS: src.FS, Root: src.At("source")}, Endpoint{FS: remote, Root: "/fixture"}, true, o)
				}
				if err == nil || os.IsNotExist(err) || !strings.Contains(err.Error(), "identity") || calls != 1 || mutations != 0 {
					t.Errorf("invalid identity authorized operation: calls=%d mutations=%d err=%v", calls, mutations, err)
				}
				if data, err := os.ReadFile(src.At("source")); err != nil || string(data) != "original" {
					t.Fatalf("local source changed: data=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestVirtualRemoteRootDoesNotRequireFileIdentity(t *testing.T) {
	client := requester.NewHTTPClient()
	client.Client.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("virtual root unexpectedly requested metadata")
		return nil, nil
	})
	remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
	entry, err := remote.Stat(context.Background(), "/")
	if err != nil || !entry.IsDir || entry.Name != "/" {
		t.Fatalf("virtual root rejected: entry=%+v err=%v", entry, err)
	}
}
