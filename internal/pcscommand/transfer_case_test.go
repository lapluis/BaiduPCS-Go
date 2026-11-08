package pcscommand

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestShareTransferAcceptsURLAuthorityCase(t *testing.T) {
	for _, tc := range []struct {
		name, input, link string
		code              string
	}{
		{"upper-host", "https://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd", "https://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd", ""},
		{"mixed-init", "https://Pan.Baidu.Com/share/init?surl=AbC_fixture&pwd=AbCd", "https://Pan.Baidu.Com/share/init?surl=AbC_fixture&pwd=AbCd", ""},
		{"upper-scheme", "HTTPS://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd", "HTTPS://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd", ""},
		{"share-text", "分享链接：HTTPS://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd 提取码：AbCd", "HTTPS://PAN.BAIDU.COM/s/1AbC_fixture?pwd=AbCd", ""},
		{"explicit-code", "https://PAN.BAIDU.COM/s/1AbC_fixture", "https://PAN.BAIDU.COM/s/1AbC_fixture", "AbCd"},
	} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", tc.name, contextual), func(t *testing.T) {
				calls, verifies, lists := 0, 0, 0
				client := requester.NewHTTPClient()
				client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
					calls++
					if req.Body != nil {
						defer req.Body.Close()
					}
					payload := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
					switch req.URL.Path {
					case "/share/verify":
						verifies++
						if err := req.ParseForm(); err != nil {
							t.Fatal(err)
						}
						if req.Form.Get("pwd") != "AbCd" || req.Header.Get("Referer") != tc.link {
							t.Error("extraction code or original link case changed")
						}
						payload = `{"errno":0}`
					case "/share/list":
						lists++
						if req.URL.Query().Get("shorturl") != "AbC_fixture" {
							t.Error("case-sensitive share identity changed")
						}
						payload = `{"errno":0,"list":[{"fs_id":1,"server_filename":"AbC.txt"}]}`
					case "/share/transfer":
						payload = `{"errno":0,"info":[{"path":"/saved/AbC.txt"}]}`
					}
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
				})
				pcs := baidupcs.NewPCSWithClient(1, client)
				params := []string{tc.input}
				if tc.code != "" {
					params = append(params, tc.code)
				}
				var result *ShareTransferResult
				var err error
				if contextual {
					result, err = ShareTransferToWithContext(context.Background(), pcs, params, "/saved", nil)
				} else {
					result, err = ShareTransferTo(pcs, params, "/saved", nil)
				}
				if err != nil || result == nil || result.Path != "/saved" || result.Filename != "AbC.txt" || calls != 5 || verifies != 1 || lists != 1 {
					t.Errorf("valid share case rejected: result=%v err=%v requests=%d verify=%d list=%d", result, err, calls, verifies, lists)
				}
				if err := ValidateShareTransferParams(params); err != nil {
					t.Errorf("preflight rejected supported case: %v", err)
				}
			})
		}
	}
}

func TestShareTransferDoesNotExtractURLInsideForeignURL(t *testing.T) {
	for _, link := range []string{
		"HTTPS://fixture.invalid/?next=https://pan.baidu.com/s/1AbC_fixture?pwd=AbCd",
		"HTTPS://pan.baidu.com@fixture.invalid/?next=https://pan.baidu.com/s/1AbC_fixture?pwd=AbCd",
		"HTTPS://fixture.invalid/s/1AbC_fixture?pwd=AbCd",
		"https://PAN.BAIDU.COM.fixture.invalid/s/1AbC_fixture?pwd=AbCd",
	} {
		if err := ValidateShareTransferParams([]string{link}); err == nil {
			t.Errorf("foreign URL accepted as share text: %s", link)
		}
	}
}
