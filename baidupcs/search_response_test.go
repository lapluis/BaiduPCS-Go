package baidupcs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestMetadataAndSearchRejectNullEntries(t *testing.T) {
	for _, operation := range []string{"meta", "search"} {
		for _, list := range []string{`[null]`, `[{"fs_id":1,"path":"/one"},null]`} {
			t.Run(operation+list, func(t *testing.T) {
				defer func() {
					if p := recover(); p != nil {
						t.Errorf("null entry caused panic: %v", p)
					}
				}()
				body := &locateBody{Reader: strings.NewReader(`{"error_code":0,"list":` + list + `}`)}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var files FileDirectoryList
				var err pcserror.Error
				if operation == "meta" {
					files, err = pcs.FilesDirectoriesBatchMeta("/one")
				} else {
					files, err = pcs.Search("/", "one", false)
				}
				if err == nil || files != nil {
					t.Fatalf("invalid response returned as success: files=%v err=%v", files, err)
				}
				if err.GetErrType() != pcserror.ErrTypeOthers || !body.closed {
					t.Fatalf("unexpected error/response ownership: err=%v closed=%v", err, body.closed)
				}
			})
		}
	}
}

func TestSearchResponseCompatibility(t *testing.T) {
	for _, mode := range []string{"file", "empty", "remote-error", "invalid-json"} {
		t.Run(mode, func(t *testing.T) {
			payload := `{"error_code":0,"list":[{"fs_id":1,"path":"/one","md5":"old","block_list":["d41d8cd98f00b204e9800998ecf8427e"]}]}`
			switch mode {
			case "empty":
				payload = `{"error_code":0,"list":[]}`
			case "remote-error":
				payload = `{"error_code":31066,"error_msg":"fixture missing"}`
			case "invalid-json":
				payload = `{`
			}
			body := &locateBody{Reader: strings.NewReader(payload)}
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("path") != "/" {
					t.Error("empty search path did not default to root")
				}
				resp := response(r, "")
				resp.Body = body
				return resp, nil
			})
			files, err := pcs.Search("", "one", false)
			if !body.closed {
				t.Fatal("search response not closed")
			}
			switch mode {
			case "file":
				if err != nil || len(files) != 1 || files[0].MD5 != "d41d8cd98f00b204e9800998ecf8427e" {
					t.Fatalf("legacy search/MD5 changed: files=%v err=%v", files, err)
				}
			case "empty":
				if err != nil || len(files) != 0 {
					t.Fatalf("empty search rejected: files=%v err=%v", files, err)
				}
			case "remote-error":
				if err == nil || err.GetRemoteErrCode() != 31066 || files != nil {
					t.Fatalf("remote error lost: files=%v err=%v", files, err)
				}
			case "invalid-json":
				if err == nil || files != nil {
					t.Fatalf("invalid JSON accepted: files=%v err=%v", files, err)
				}
			}
		})
	}
}

type searchCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r searchCancelReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.cancel()
	return n, err
}

func TestSearchCancellationDuringSuccessfulResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := &locateBody{Reader: searchCancelReader{strings.NewReader(`{"error_code":0,"list":[]}`), cancel}}
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		resp := response(r, "")
		resp.Body = body
		return resp, nil
	})
	files, err := pcs.SearchWithContext(ctx, "/", "one", false)
	if !errors.Is(err, context.Canceled) || files != nil || !body.closed {
		t.Fatalf("canceled success accepted: files=%v err=%v closed=%v", files, err, body.closed)
	}
}
