package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestListingRejectsInvalidChildPaths(t *testing.T) {
	for _, raw := range []string{"", "/Temp", "/", "/Other/one", "/Temp/sub/one", "Temp/one", "/Temp/sub/../one", "/Temp//one", "/Temp/one/"} {
		for _, cached := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cached=%t", raw, cached), func(t *testing.T) {
				body := &locateBody{Reader: strings.NewReader(fmt.Sprintf(`{"errno":0,"list":[{"fs_id":1,"path":%q,"isdir":1}]}`, raw))}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					resp := response(r, "")
					resp.Body = body
					return resp, nil
				})
				var files FileDirectoryList
				var err pcserror.Error
				if cached {
					files, err = pcs.CacheFilesDirectoriesList("/Temp", nil)
				} else {
					files, err = pcs.FilesDirectoriesListWithContext(context.Background(), "/Temp", nil)
				}
				if err == nil || files != nil || !body.closed {
					t.Fatalf("invalid child path accepted: count=%d err=%v closed=%t", len(files), err, body.closed)
				}
			})
		}
	}
}

func TestListingRejectsRepeatedPathWithDifferentID(t *testing.T) {
	for _, laterPage := range []bool{false, true} {
		t.Run(fmt.Sprint(laterPage), func(t *testing.T) {
			calls := 0
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				calls++
				if laterPage && calls == 1 {
					return response(r, pageBody(1, maxListNum)), nil
				}
				list := `[{"fs_id":1001,"path":"/Temp/1"}]`
				if !laterPage {
					list = `[{"fs_id":1,"path":"/Temp/one"},{"fs_id":2,"path":"/Temp/one"}]`
				}
				return response(r, `{"errno":0,"list":`+list+`}`), nil
			})
			files, err := pcs.FilesDirectoriesList("/Temp", nil)
			if err == nil || files != nil {
				t.Fatalf("repeated path accepted: calls=%d count=%d err=%v", calls, len(files), err)
			}
		})
	}
}

func TestListingPreservesValidChildPaths(t *testing.T) {
	for _, tc := range [][2]string{
		{"", "/one"}, {"/", "/直至魔女消逝"}, {"/Temp/", "/Temp/.hidden"},
		{"/Temp/.", "/Temp/file"}, {"Temp", "Temp/file"}, {"Temp", "/Temp/file"},
	} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				return response(r, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":1,"path":%q,"size":1}]}`, tc[1])), nil
			})
			files, err := pcs.FilesDirectoriesList(tc[0], nil)
			if err != nil || len(files) != 1 || files[0].Path != tc[1] {
				t.Fatalf("valid child changed: count=%d err=%v", len(files), err)
			}
		})
	}
}

func TestListingRejectsRelativeAbsoluteDuplicate(t *testing.T) {
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		return response(r, `{"errno":0,"list":[{"fs_id":1,"path":"Temp/one"},{"fs_id":2,"path":"/Temp/one"}]}`), nil
	})
	if files, err := pcs.FilesDirectoriesList("Temp", nil); err == nil || files != nil {
		t.Fatal("same path in relative and absolute form accepted")
	}
}

func TestRecursiveListingRejectsSelfBeforeDescending(t *testing.T) {
	calls, visited, failures := 0, 0, 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Query().Get("method") == "meta" {
			return response(r, `{"error_code":0,"list":[{"fs_id":1,"path":"/Temp","isdir":1}]}`), nil
		}
		return response(r, `{"errno":0,"list":[{"fs_id":1,"path":"/Temp","isdir":1}]}`), nil
	})
	pcs.FilesDirectoriesRecurseList("/Temp", nil, func(depth int, path string, file *FileDirectory, err pcserror.Error) bool {
		if err != nil {
			failures++
			return false
		}
		visited++
		// Bound the baseline fixture: an unfixed implementation must not recurse forever.
		return visited < 3
	})
	if calls != 2 || visited != 1 || failures != 1 {
		t.Fatalf("self entry descended: calls=%d visited=%d failures=%d", calls, visited, failures)
	}
}
