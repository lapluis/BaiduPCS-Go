package baidupcs

import (
	"net/http"
	"strings"
	"testing"
)

func TestPanAPIDownloadStopsOnFailedSignaturePageAndCanRetry(t *testing.T) {
	pages, downloads := 0, 0
	var bodies []*uploadDigestBody
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		code := http.StatusOK
		payload := `{"errno":0,"dlink":[{"fs_id":"7","dlink":"https://download.fixture.invalid/file"}]}`
		if r.URL.Path == "/disk/home" {
			pages++
			payload = `{"sign1":"fixture","sign3":"key","timestamp":123,}`
			if pages == 1 {
				code = http.StatusServiceUnavailable
			}
		} else {
			downloads++
			if r.URL.Path != "/api/download" {
				t.Error("unexpected API request", r.URL)
			}
		}
		body := &uploadDigestBody{Reader: strings.NewReader(payload)}
		bodies = append(bodies, body)
		return &http.Response{StatusCode: code, Header: make(http.Header), Body: body, Request: r}, nil
	})
	links, err := pcs.LocatePanAPIDownload(7)
	if err == nil || links != nil || pages != 1 || downloads != 0 {
		t.Fatalf("failed signature page reached download API: links=%v err=%v pages=%d downloads=%d", links, err, pages, downloads)
	}
	links, err = pcs.LocatePanAPIDownload(7)
	if err != nil || len(links) != 1 || links[0].FsID != "7" || pages != 2 || downloads != 1 {
		t.Fatal("signature recovery failed", links, err, pages, downloads)
	}
	links, err = pcs.LocatePanAPIDownload(7)
	if err != nil || len(links) != 1 || pages != 2 || downloads != 2 {
		t.Fatal("valid signature cache changed", links, err, pages, downloads)
	}
	for _, body := range bodies {
		if !body.closed {
			t.Fatal("signature/API response body leaked")
		}
	}
}
