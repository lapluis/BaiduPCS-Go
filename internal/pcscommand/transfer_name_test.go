package pcscommand

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestShareCollectRejectsPathAsFilenameBeforeMutation(t *testing.T) {
	for _, name := range []string{"../../escape", "nested/file", "/absolute", ".", ".."} {
		for _, withContext := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", name, withContext), func(t *testing.T) {
				mutations := 0
				var paths []string
				client := requester.NewHTTPClient()
				client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					if req.Body != nil {
						defer req.Body.Close()
					}
					body := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
					switch req.URL.Path {
					case "/share/verify":
						body = `{"errno":0}`
					case "/share/list":
						encoded, err := json.Marshal(map[string]interface{}{"errno": 0, "list": []map[string]interface{}{
							{"fs_id": 1, "server_filename": name}, {"fs_id": 2, "server_filename": "second.txt"},
						}})
						if err != nil {
							t.Fatal(err)
						}
						body = string(encoded)
					case "/rest/2.0/pcs/file":
						mutations++
						paths = append(paths, req.URL.Query().Get("path"))
						body = `{}`
					case "/share/transfer":
						mutations++
						if err := req.ParseForm(); err != nil {
							t.Fatal(err)
						}
						paths = append(paths, req.Form.Get("path"))
						body = `{"errno":0,"info":[{"path":"/saved/first.txt"},{"path":"/saved/second.txt"}]}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				})
				pcs := baidupcs.NewPCSWithClient(1, client)
				params := []string{"https://pan.baidu.com/s/1fixture", "abcd"}
				var result *ShareTransferResult
				var err error
				if withContext {
					result, err = ShareTransferToWithContext(context.Background(), pcs, params, "/saved", &baidupcs.TransferOption{Collect: true})
				} else {
					result, err = ShareTransferTo(pcs, params, "/saved", &baidupcs.TransferOption{Collect: true})
				}
				if err == nil || result != nil || mutations != 0 {
					t.Fatalf("unsafe name authorized mutations: result=%v err=%v mutations=%d paths=%q", result, err, mutations, paths)
				}
			})
		}
	}
}
