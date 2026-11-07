package baidupcs

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func querySizeFixture(pcs *BaiduPCS, operation string, contextual bool) (FileDirectoryList, pcserror.Error) {
	ctx := context.Background()
	switch operation {
	case "meta":
		var file *FileDirectory
		var err pcserror.Error
		if contextual {
			file, err = pcs.FilesDirectoriesMetaWithContext(ctx, "/one")
		} else {
			file, err = pcs.FilesDirectoriesMeta("/one")
		}
		if file == nil {
			return nil, err
		}
		return FileDirectoryList{file}, err
	case "batch":
		if contextual {
			return pcs.FilesDirectoriesBatchMetaWithContext(ctx, "/one")
		}
		return pcs.FilesDirectoriesBatchMeta("/one")
	case "search":
		if contextual {
			return pcs.SearchWithContext(ctx, "/", "one", false)
		}
		return pcs.Search("/", "one", false)
	default:
		if contextual {
			return pcs.FilesDirectoriesListWithContext(ctx, "/", DefaultOrderOptions)
		}
		return pcs.FilesDirectoriesList("/", DefaultOrderOptions)
	}
}

func TestFileQueriesRejectNegativeSizes(t *testing.T) {
	for _, operation := range []string{"meta", "batch", "search", "list"} {
		for _, contextual := range []bool{false, true} {
			for _, tc := range []struct {
				name string
				size int64
				dir  int
			}{
				{"negative-file", -1, 0},
				{"negative-directory", -1, 1},
				{"minimum-int64", math.MinInt64, 0},
				{"zero-file", 0, 0},
				{"zero-directory", 0, 1},
				{"large-file", math.MaxInt64, 0},
			} {
				t.Run(fmt.Sprintf("%s/context=%t/%s", operation, contextual, tc.name), func(t *testing.T) {
					payload := fmt.Sprintf(`{"error_code":0,"errno":0,"list":[{"fs_id":1,"path":"/one","size":%d,"isdir":%d}]}`, tc.size, tc.dir)
					body := &locateBody{Reader: strings.NewReader(payload)}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					files, err := querySizeFixture(pcs, operation, contextual)
					if tc.size < 0 {
						if err == nil || err.GetErrType() != pcserror.ErrTypeOthers || files != nil {
							t.Errorf("negative size accepted: count=%d err=%v", len(files), err)
						}
					} else if err != nil || len(files) != 1 || files[0].Size != tc.size || files[0].Isdir != (tc.dir == 1) {
						t.Errorf("valid size rejected or changed: count=%d err=%v", len(files), err)
					}
					if !body.closed {
						t.Error("response body not closed")
					}
				})
			}
		}
	}
}

func TestNegativeSizeOnLaterPageDiscardsListing(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, pageBody(1, maxListNum)), nil
		}
		return response(r, `{"errno":0,"list":[{"fs_id":1001,"path":"/Temp/bad","size":-1}]}`), nil
	})
	files, err := pcs.FilesDirectoriesListWithContext(context.Background(), "/Temp", DefaultOrderOptions)
	if calls != 2 || err == nil || files != nil || err.GetErrType() != pcserror.ErrTypeOthers {
		t.Fatalf("invalid later page published a partial snapshot: calls=%d count=%d err=%v", calls, len(files), err)
	}
}
