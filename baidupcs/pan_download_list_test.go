package baidupcs

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

type panDownloadListCalls struct {
	pages, downloads int
	bodies           []*uploadDigestBody
}

func panDownloadListFixture(t *testing.T, payload func(int) string) (*BaiduPCS, *panDownloadListCalls) {
	t.Helper()
	calls := &panDownloadListCalls{}
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		var data string
		if r.URL.Path == "/disk/home" {
			calls.pages++
			data = `{"sign1":"fixture","sign3":"key","timestamp":123,}`
		} else {
			calls.downloads++
			if r.URL.Path != "/api/download" {
				t.Error("unexpected request", r.URL)
			}
			data = payload(calls.downloads)
		}
		body := &uploadDigestBody{Reader: strings.NewReader(data)}
		calls.bodies = append(calls.bodies, body)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body, Request: r}, nil
	})
	t.Cleanup(func() {
		for _, body := range calls.bodies {
			if !body.closed {
				t.Error("download/page response body leaked")
			}
		}
	})
	return pcs, calls
}

func TestPanAPIDownloadRejectsNullLinkEntries(t *testing.T) {
	item := `{"fs_id":"7","dlink":"https://download.fixture.invalid/file"}`
	for _, tc := range []struct {
		name, list string
		count      int
		invalid    bool
	}{
		{"empty", `[]`, 0, false},
		{"valid", `[` + item + `]`, 1, false},
		{"null", `[null]`, 0, true},
		{"null-first", `[null,` + item + `]`, 0, true},
		{"null-last", `[` + item + `,null]`, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pcs, calls := panDownloadListFixture(t, func(attempt int) string {
				if attempt == 1 {
					return `{"errno":0,"dlink":` + tc.list + `}`
				}
				return `{"errno":0,"dlink":[` + item + `]}`
			})
			links, err := pcs.LocatePanAPIDownload(7)
			if tc.invalid {
				if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || links != nil || !strings.Contains(err.Error(), "null entry") {
					t.Fatalf("invalid link list accepted: links=%v err=%v", links, err)
				}
			} else if err != nil || len(links) != tc.count {
				t.Fatal("valid link list rejected", links, err)
			}
			links, err = pcs.LocatePanAPIDownload(7)
			if err != nil || len(links) != 1 || links[0] == nil || links[0].FsID != "7" || calls.pages != 1 || calls.downloads != 2 {
				t.Fatal("link retry damaged valid signature cache", links, err, calls.pages, calls.downloads)
			}
		})
	}
}

func TestPanAPIDownloadNullEntriesDoNotMaskExpiredSignature(t *testing.T) {
	for _, code := range []int{112, 113} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			pcs, calls := panDownloadListFixture(t, func(attempt int) string {
				if attempt == 1 {
					return fmt.Sprintf(`{"errno":%d,"dlink":[null]}`, code)
				}
				return `{"errno":0,"dlink":[{"fs_id":"7","dlink":"https://download.fixture.invalid/file"}]}`
			})
			links, err := pcs.LocatePanAPIDownload(7)
			if err == nil || err.GetErrType() != pcserror.ErrTypeRemoteError || err.GetRemoteErrCode() != code || links != nil {
				t.Fatal("signature error masked", links, err)
			}
			links, err = pcs.LocatePanAPIDownload(7)
			if err != nil || len(links) != 1 || calls.pages != 2 || calls.downloads != 2 {
				t.Fatal("expired signature was not refreshed", links, err, calls.pages, calls.downloads)
			}
		})
	}
}
