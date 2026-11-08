package pcscommand

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestShareTransferVerificationUsesFullMillisecondTimestamp(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		t.Run(fmt.Sprintf("context=%t", contextual), func(t *testing.T) {
			requests, verifies := 0, 0
			var timestamp string
			client := requester.NewHTTPClient()
			client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
				requests++
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
				switch req.URL.Path {
				case "/share/verify":
					verifies++
					timestamp = req.URL.Query().Get("time")
					payload = `{"errno":0}`
				case "/share/list":
					payload = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a.txt"}]}`
				case "/share/transfer":
					payload = `{"errno":0,"info":[{"path":"/saved/a.txt"}]}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			pcs := baidupcs.NewPCSWithClient(1, client)
			params := []string{"https://pan.baidu.com/s/1fixture", "abcd"}
			before := time.Now().UnixMilli()
			var result *ShareTransferResult
			var err error
			if contextual {
				result, err = ShareTransferToWithContext(context.Background(), pcs, params, "/saved", nil)
			} else {
				result, err = ShareTransferTo(pcs, params, "/saved", nil)
			}
			after := time.Now().UnixMilli()
			if err != nil || result == nil || result.Path != "/saved" || requests != 5 || verifies != 1 {
				t.Fatalf("transfer flow changed: result=%v err=%v requests=%d verifies=%d", result, err, requests, verifies)
			}
			value, parseErr := strconv.ParseInt(timestamp, 10, 64)
			if parseErr != nil || value < before || value > after {
				t.Fatalf("verification timestamp was truncated: got=%q want within [%d,%d] parseErr=%v", timestamp, before, after, parseErr)
			}
		})
	}
}
