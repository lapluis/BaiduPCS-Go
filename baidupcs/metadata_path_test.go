package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestMetadataRejectsUnrequestedPaths(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, contextual := range []bool{false, true} {
			for _, wrong := range []string{"", "/other", "/one/child"} {
				t.Run(fmt.Sprintf("batch=%t/context=%t/%s", batch, contextual, wrong), func(t *testing.T) {
					list := fmt.Sprintf(`[{"fs_id":1,"path":%q,"size":1}]`, wrong)
					if batch {
						list = fmt.Sprintf(`[{"fs_id":2,"path":"/one","size":1},{"fs_id":1,"path":%q,"size":1}]`, wrong)
					}
					body := &locateBody{Reader: strings.NewReader(`{"error_code":0,"list":` + list + `}`)}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					var err pcserror.Error
					if batch {
						var files FileDirectoryList
						if contextual {
							files, err = pcs.FilesDirectoriesBatchMetaWithContext(context.Background(), "/one", "/two")
						} else {
							files, err = pcs.FilesDirectoriesBatchMeta("/one", "/two")
						}
						if files != nil {
							t.Error("unexpected metadata or partial results returned")
						}
					} else {
						var file *FileDirectory
						if contextual {
							file, err = pcs.FilesDirectoriesMetaWithContext(context.Background(), "/one")
						} else {
							file, err = pcs.FilesDirectoriesMeta("/one")
						}
						if file != nil {
							t.Error("unexpected metadata returned")
						}
					}
					if err == nil || !body.closed {
						t.Fatalf("mismatched metadata accepted: err=%v closed=%t", err, body.closed)
					}
				})
			}
		}
	}
}

func TestMetadataMatchingPathsPreserveRepresentationAndOrder(t *testing.T) {
	for _, tc := range []struct {
		requested, returned []string
	}{
		{[]string{"/one", "/two"}, []string{"/two", "/one"}},
		{[]string{"/Temp/./one", "two"}, []string{"/two", "/Temp/one"}},
		{[]string{"Temp/one"}, []string{"Temp/one"}},
		{[]string{""}, []string{"/"}},
	} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/context=%t", tc.requested, contextual), func(t *testing.T) {
				var entries []string
				for i, path := range tc.returned {
					entries = append(entries, fmt.Sprintf(`{"fs_id":%d,"path":%q,"size":1}`, i+1, path))
				}
				pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
					return response(r, `{"error_code":0,"list":[`+strings.Join(entries, ",")+`]}`), nil
				})
				var files FileDirectoryList
				var err pcserror.Error
				if contextual {
					files, err = pcs.FilesDirectoriesBatchMetaWithContext(context.Background(), tc.requested...)
				} else {
					files, err = pcs.FilesDirectoriesBatchMeta(tc.requested...)
				}
				if err != nil || len(files) != len(tc.returned) {
					t.Fatal("matching metadata rejected", err)
				}
				for i, path := range tc.returned {
					if files[i].Path != path {
						t.Fatal("response order or path representation changed")
					}
				}
			})
		}
	}
}

func TestRecursiveListingRejectsWrongRootMetadata(t *testing.T) {
	calls, visits, failures := 0, 0, 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		return response(r, `{"error_code":0,"list":[{"fs_id":1,"path":"/other","size":3}]}`), nil
	})
	files := pcs.FilesDirectoriesRecurseList("/one", nil, func(depth int, path string, file *FileDirectory, err pcserror.Error) bool {
		if err != nil {
			failures++
		} else {
			visits++
		}
		return true
	})
	if files != nil || calls != 1 || visits != 0 || failures != 1 {
		t.Fatalf("wrong root visited: count=%d calls=%d visits=%d failures=%d", len(files), calls, visits, failures)
	}
}
