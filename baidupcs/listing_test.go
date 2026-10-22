package baidupcs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fixturePCS(fn fixtureTransport) *BaiduPCS {
	c := requester.NewHTTPClient()
	c.SetKeepAlive(false)
	c.Client.Transport = fn
	return NewPCSWithClient(1, c)
}
func response(req *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
}
func pageBody(start, n int) string {
	entries := make([]map[string]interface{}, 0, n)
	for i := 0; i < n; i++ {
		entries = append(entries, map[string]interface{}{"fs_id": start + i, "path": fmt.Sprintf("/Temp/%d", start+i), "size": 1})
	}
	b, _ := json.Marshal(map[string]interface{}{"errno": 0, "list": entries})
	return string(b)
}
func TestListingPaginationAndFailures(t *testing.T) {
	for _, mode := range []string{"complete", "duplicate", "limit", "network", "missing-list", "null-entry"} {
		t.Run(mode, func(t *testing.T) {
			pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
				p, _ := strconv.Atoi(req.URL.Query().Get("page"))
				if mode == "missing-list" {
					return response(req, `{"errno":0}`), nil
				}
				if mode == "null-entry" {
					return response(req, `{"errno":0,"list":[null]}`), nil
				}
				if p == 1 {
					return response(req, pageBody(1, maxListNum)), nil
				}
				switch mode {
				case "network":
					return nil, errors.New("injected network error")
				case "duplicate":
					return response(req, pageBody(1, maxListNum)), nil
				case "limit":
					return response(req, pageBody(maxListNum+1, maxListNum)), nil
				default:
					return response(req, pageBody(maxListNum+1, 1)), nil
				}
			})
			got, err := pcs.filesDirectoriesList("/Temp", DefaultOrderOptions, 2)
			if mode == "complete" {
				if err != nil || len(got) != maxListNum+1 {
					t.Fatal(len(got), err)
				}
			} else if err == nil {
				t.Fatal("incomplete listing reported success")
			}
		})
	}
}
func TestCopyAndMovePropagateTransportFailure(t *testing.T) {
	pcs := fixturePCS(func(*http.Request) (*http.Response, error) { return nil, errors.New("injected request failure") })
	if err := pcs.Copy(&CpMvJSON{From: "/Temp/a", To: "/Temp/b"}); err == nil {
		t.Fatal("copy swallowed request failure")
	}
	if err := pcs.Move(&CpMvJSON{From: "/Temp/a", To: "/Temp/b"}); err == nil {
		t.Fatal("move swallowed request failure")
	}
}
