package rc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestServerMoveFinalVerificationPreservesErrors(t *testing.T) {
	for _, mode := range []string{"cancel", "cancel-missing", "network", "timeout", "source-remains", "success"} {
		for _, engine := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/engine=%t", mode, engine), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				failure := errors.New("fixture final metadata unavailable")
				calls := 0
				client := requester.NewHTTPClient()
				client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
					if r.Body != nil {
						defer r.Body.Close()
					}
					calls++
					payload := `{"error_code":0}`
					switch calls {
					case 1:
						payload = `{"error_code":0,"list":[{"path":"/source","fs_id":7,"size":3,"mtime":0}]}`
					case 2:
						payload = `{"error_code":31066}`
					case 3:
						if r.URL.Query().Get("method") != "move" {
							return nil, fmt.Errorf("unexpected mutation %s", r.URL)
						}
					case 4:
						payload = `{"error_code":0,"list":[{"path":"/target","fs_id":7,"size":3,"mtime":0}]}`
					case 5:
						switch mode {
						case "cancel":
							cancel()
							return nil, ctx.Err()
						case "cancel-missing":
							cancel()
							payload = `{"error_code":31066}`
						case "network":
							return nil, failure
						case "timeout":
							return nil, context.DeadlineExceeded
						case "source-remains":
							payload = `{"error_code":0,"list":[{"path":"/source","fs_id":7,"size":3,"mtime":0}]}`
						case "success":
							payload = `{"error_code":31066}`
						}
					default:
						return nil, fmt.Errorf("unexpected extra request %d", calls)
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &emptyUploadResultBody{Reader: strings.NewReader(payload)}, Request: r}, nil
				})
				remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
				src, dst := Endpoint{FS: remote, Root: "/source"}, Endpoint{FS: remote, Root: "/target"}
				a := Action{Kind: "copy", Source: &Entry{Size: 3, ID: "7", ModTime: time.Unix(0, 0)}}
				var err error
				if engine {
					result, e := Execute(ctx, &Plan{Source: src, Destination: dst, Move: true, Actions: []Action{a}}, opts())
					err = e
					wantFailed, wantCanceled, wantCopied := 1, 0, 0
					if mode == "cancel" || mode == "cancel-missing" {
						wantFailed, wantCanceled = 0, 1
					}
					if mode == "success" {
						wantFailed, wantCopied = 0, 1
					}
					if result.Failed != wantFailed || result.Canceled != wantCanceled || result.Copied != wantCopied {
						t.Errorf("final verification misclassified: %+v err=%v", result, err)
					}
				} else {
					var used bool
					used, err = tryServerMove(ctx, src, dst, a, opts())
					if !used {
						t.Fatal("server move not used")
					}
				}
				if calls != 5 {
					t.Fatalf("unexpected requests %d: %v", calls, err)
				}
				switch mode {
				case "cancel", "cancel-missing":
					if !errors.Is(err, context.Canceled) {
						t.Fatal("cancellation cause discarded", err)
					}
				case "network":
					if !errors.Is(err, failure) {
						t.Fatal("metadata failure discarded", err)
					}
				case "timeout":
					if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
						t.Fatal("request timeout lost or parent canceled", err)
					}
				case "source-remains":
					if err == nil {
						t.Fatal("source existence not checked")
					}
				case "success":
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}
