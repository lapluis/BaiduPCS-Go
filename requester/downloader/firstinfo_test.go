package downloader

import (
	"net/http"
	"testing"
)

func TestFirstInfoKeepsMatchingContentLength(t *testing.T) {
	for _, resp := range []*http.Response{nil, {ContentLength: 123, Header: http.Header{}}} {
		if got := NewDownloadFirstInfoByResp(123, resp); got.ContentLength != 123 {
			t.Fatal(got)
		}
	}
}
