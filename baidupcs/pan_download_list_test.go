package baidupcs

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestPanAPIDownloadRejectsUnusableLinksAndRecovers(t *testing.T) {
	const item = `{"fs_id":"7","dlink":"https://download.fixture.invalid/file"}`
	for _, raw := range []string{"", "/relative", "http://:80/file", "ftp://fixture.invalid/file", "http://user:synthetic@fixture.invalid/file", "http://fixture.invalid/%zz"} {
		encoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		bad := `{"fs_id":"7","dlink":` + string(encoded) + `}`
		for _, list := range []string{`[` + bad + `]`, `[` + item + `,` + bad + `]`, `[` + bad + `,` + item + `]`} {
			t.Run(list, func(t *testing.T) {
				pcs, calls := panDownloadListFixture(t, func(attempt int) string {
					if attempt == 1 {
						return `{"errno":0,"dlink":` + list + `}`
					}
					return `{"errno":0,"dlink":[` + item + `]}`
				})
				links, err := pcs.LocatePanAPIDownload(7)
				if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || links != nil {
					t.Fatalf("unusable list returned as success or partial result: links=%v err=%v", links, err)
				}
				links, err = pcs.LocatePanAPIDownload(7)
				if err != nil || len(links) != 1 || links[0].Dlink != "https://download.fixture.invalid/file" || calls.pages != 1 || calls.downloads != 2 {
					t.Fatalf("healthy retry or signature cache damaged: links=%v err=%v pages=%d downloads=%d", links, err, calls.pages, calls.downloads)
				}
			})
		}
	}
}

func TestPanAPIDownloadPreservesLinkRepresentation(t *testing.T) {
	for _, raw := range []string{"http://fixture.invalid:8080/a%2Fb?token=synthetic%2Bvalue&x=1&x=2", "https://fixture.invalid/file", "//fixture.invalid/file?sign=synthetic"} {
		t.Run(raw, func(t *testing.T) {
			encoded, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			pcs, _ := panDownloadListFixture(t, func(int) string {
				return `{"errno":0,"dlink":[{"fs_id":"7","dlink":` + string(encoded) + `}]}`
			})
			links, err := pcs.LocatePanAPIDownload(7)
			if err != nil || len(links) != 1 || links[0].Dlink != raw || links[0].FsID != "7" {
				t.Fatalf("valid URL representation changed: links=%v err=%v", links, err)
			}
		})
	}
}

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
