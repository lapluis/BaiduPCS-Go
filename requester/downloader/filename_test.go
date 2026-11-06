package downloader

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type filenameTransport struct{ header, finalURL string }

func (tr filenameTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if tr.finalURL != "" {
		clone := r.Clone(r.Context())
		clone.URL, _ = url.Parse(tr.finalURL)
		r = clone
	}
	header := make(http.Header)
	if tr.header != "" {
		header.Set("Content-Disposition", tr.header)
	}
	return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
}

func TestGetFileNamePreservesNamesAndUsesURLPath(t *testing.T) {
	for _, tc := range []struct{ name, uri, disposition, finalURL, want string }{
		{"plus", "fixture://host/fallback", `attachment; filename="a+b.mkv"`, "", "a+b.mkv"},
		{"literal-percent", "fixture://host/fallback", `attachment; filename="100% complete.mkv"`, "", "100% complete.mkv"},
		{"encoded-looking-literal", "fixture://host/fallback", `attachment; filename="a%20b.mkv"`, "", "a%20b.mkv"},
		{"extended-unicode", "fixture://host/fallback", `attachment; filename*=UTF-8''%E4%B8%AD%E6%96%87%2B%2520.mkv`, "", "中文+%20.mkv"},
		{"query-fragment", "fixture://host/dir/movie.mkv?token=fixture#fragment", "", "", "movie.mkv"},
		{"escaped-path", "fixture://host/dir/a%20b%2Bc.mkv?x=1", "attachment", "", "a b+c.mkv"},
		{"redirected-path", "fixture://host/download?id=1", "", "fixture://cdn/dir/final.mkv?x=2", "final.mkv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := requester.NewHTTPClient()
			client.Client.Transport = filenameTransport{tc.disposition, tc.finalURL}
			got, err := GetFileName(tc.uri, client)
			if err != nil || got != tc.want {
				t.Fatalf("name=%q want=%q err=%v", got, tc.want, err)
			}
		})
	}
}
