package rc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestCanceledHTTPMetadataErrorDoesNotMeanMissing(t *testing.T) {
	for _, payload := range []string{`{"error_code":31066}`, `{"errno":-9}`} {
		t.Run(payload, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			client := requester.NewHTTPClient()
			client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Body != nil {
					defer req.Body.Close()
				}
				body := mutationCancelReader{Reader: strings.NewReader(payload), cancel: cancel}
				return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(body), Request: req}, nil
			})
			remote := &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}
			entry, err := remote.Stat(ctx, "/fixture")
			if !errors.Is(err, context.Canceled) || os.IsNotExist(err) || calls != 1 || entry.ID != "" {
				t.Fatalf("canceled response treated as absence: calls=%d entry=%+v err=%v", calls, entry, err)
			}
		})
	}
}
