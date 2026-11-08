package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestBatchMetadataBoundsResponsePathMultiplicity(t *testing.T) {
	for _, tc := range []struct {
		name                string
		requested, returned []string
		valid               bool
	}{
		{"repeated-single", []string{"/one"}, []string{"/one", "/one"}, false},
		{"repeated-replaces-other", []string{"/one", "/two"}, []string{"/one", "/one"}, false},
		{"rooted-alias", []string{"/one"}, []string{"/one", "one"}, false},
		{"cleaned-alias", []string{"/one"}, []string{"/one", "/dir/../one"}, false},
		{"root-alias", []string{""}, []string{"/", "."}, false},
		{"exceeds-repeated-request", []string{"/one", "one"}, []string{"/one", "one", "/one"}, false},
		{"repeated-request", []string{"/one", "one"}, []string{"one", "/one"}, true},
		{"deduplicated-response", []string{"/one", "one"}, []string{"/one"}, true},
		{"partial-response", []string{"/one", "/two"}, []string{"/one"}, true},
		{"reordered-response", []string{"/one", "/two"}, []string{"/two", "/one"}, true},
		{"empty-response", []string{"/one"}, nil, true},
	} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", tc.name, contextual), func(t *testing.T) {
				entries := make([]string, 0, len(tc.returned))
				for i, returned := range tc.returned {
					entries = append(entries, fmt.Sprintf(`{"fs_id":%d,"path":%q,"size":1}`, i+1, returned))
				}
				body := &locateBody{Reader: strings.NewReader(`{"error_code":0,"list":[` + strings.Join(entries, ",") + `]}`)}
				calls := 0
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				var files FileDirectoryList
				var err pcserror.Error
				if contextual {
					files, err = pcs.FilesDirectoriesBatchMetaWithContext(context.Background(), tc.requested...)
				} else {
					files, err = pcs.FilesDirectoriesBatchMeta(tc.requested...)
				}
				if !body.closed || calls != 1 {
					t.Errorf("response lifetime changed: closed=%t calls=%d", body.closed, calls)
				}
				if !tc.valid {
					if err == nil || files != nil {
						t.Fatalf("excess metadata accepted: count=%d err=%v", len(files), err)
					}
					return
				}
				if err != nil || len(files) != len(tc.returned) {
					t.Fatalf("valid metadata rejected: count=%d err=%v", len(files), err)
				}
				for i, returned := range tc.returned {
					if files[i].Path != returned || files[i].FsID != int64(i+1) {
						t.Errorf("response representation or order changed: %+v", files[i])
					}
				}
			})
		}
	}
}
