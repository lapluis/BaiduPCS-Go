package pcscommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestShareCollectFallbackPreservesVerificationFailure(t *testing.T) {
	for _, mode := range []string{"network", "timeout", "malformed", "missing", "file", "directory"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", mode, contextual), func(t *testing.T) {
				ctx := context.Background()
				cause := errors.New("fixture metadata lookup failed")
				mkdirCalls, metaCalls, transfers := 0, 0, 0
				const directory = "/saved/a.txt等文件"
				client := requester.NewHTTPClient()
				client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					if req.Body != nil {
						defer req.Body.Close()
					}
					payload := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
					switch req.URL.Path {
					case "/share/verify":
						payload = `{"errno":0}`
					case "/share/list":
						payload = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a.txt"},{"fs_id":2,"server_filename":"b.txt"}]}`
					case "/rest/2.0/pcs/file":
						switch req.URL.Query().Get("method") {
						case "mkdir":
							mkdirCalls++
							payload = `{"error_code":31061}`
						case "meta":
							metaCalls++
							switch mode {
							case "network":
								return nil, cause
							case "timeout":
								return nil, context.DeadlineExceeded
							case "malformed":
								payload = `{"error_code":0,"list":[`
							case "missing":
								payload = `{"error_code":31066}`
							case "file":
								payload = `{"error_code":0,"list":[{"fs_id":4,"path":"` + directory + `","isdir":0,"size":3}]}`
							case "directory":
								payload = `{"error_code":0,"list":[{"fs_id":4,"path":"` + directory + `","isdir":1}]}`
							}
						default:
							t.Fatalf("unexpected PCS method: %s", req.URL.Query().Get("method"))
						}
					case "/share/transfer":
						transfers++
						payload = `{"errno":0,"info":[{"path":"` + directory + `/a.txt"},{"path":"` + directory + `/b.txt"}]}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				pcs := baidupcs.NewPCSWithClient(1, client)
				params := []string{"https://pan.baidu.com/s/1fixture", "abcd"}
				var result *ShareTransferResult
				var err error
				if contextual {
					result, err = ShareTransferToWithContext(ctx, pcs, params, "/saved", &baidupcs.TransferOption{Collect: true})
				} else {
					result, err = ShareTransferTo(pcs, params, "/saved", &baidupcs.TransferOption{Collect: true})
				}
				if mkdirCalls != 1 || metaCalls != 1 || ctx.Err() != nil {
					t.Fatalf("fallback flow changed: mkdir=%d meta=%d ctx=%v", mkdirCalls, metaCalls, ctx.Err())
				}
				if mode == "directory" {
					if err != nil || result == nil || result.Path != directory || transfers != 1 {
						t.Fatalf("existing directory not reused: result=%v err=%v transfers=%d", result, err, transfers)
					}
					if !reflect.DeepEqual(result.downloadPaths(), []string{directory + "/a.txt", directory + "/b.txt"}) {
						t.Fatal("fallback changed download paths")
					}
					return
				}
				if err == nil || result != nil || transfers != 0 {
					t.Fatalf("unverified directory authorized transfer: result=%v err=%v transfers=%d", result, err, transfers)
				}
				var original pcserror.Error
				if !errors.As(err, &original) || original.GetOperation() != baidupcs.OperationMkdir || original.GetRemoteErrCode() != 31061 {
					t.Fatalf("initial mkdir error lost: %v", err)
				}
				switch mode {
				case "network":
					if !errors.Is(err, cause) {
						t.Fatalf("metadata failure lost: %v", err)
					}
				case "timeout":
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("metadata timeout lost: %v", err)
					}
				case "malformed":
					var syntax *json.SyntaxError
					if !errors.As(err, &syntax) && !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("metadata decode error lost: %v", err)
					}
				case "missing":
					if !strings.Contains(err.Error(), "31066") {
						t.Fatalf("metadata remote failure lost: %v", err)
					}
				}
			})
		}
	}
}
