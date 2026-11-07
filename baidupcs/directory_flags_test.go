package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestInvalidDirectoryFlagOnLaterPageDiscardsListing(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return response(r, pageBody(1, maxListNum)), nil
		}
		return response(r, `{"errno":0,"list":[{"fs_id":1001,"path":"/Temp/invalid","size":0,"isdir":2}]}`), nil
	})
	files, err := pcs.FilesDirectoriesListWithContext(context.Background(), "/Temp", nil)
	if err == nil || files != nil || calls != 2 {
		t.Fatalf("invalid later page published partial results: count=%d calls=%d err=%v", len(files), calls, err)
	}
}

func TestInvalidDirectoryFlagsDoNotFillCache(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
		calls++
		flag := 1
		if calls == 1 {
			flag = 2
		}
		return response(r, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":1,"path":"/Temp/one","size":0,"isdir":%d}]}`, flag)), nil
	})
	if files, err := pcs.CacheFilesDirectoriesList("/Temp", nil); err == nil || files != nil {
		t.Fatal("invalid directory flag entered cache")
	}
	for i := 0; i < 2; i++ {
		files, err := pcs.CacheFilesDirectoriesList("/Temp", nil)
		if err != nil || len(files) != 1 || !files[0].Isdir || calls != 2 {
			t.Fatalf("healthy retry or cache reuse failed: count=%d calls=%d err=%v", len(files), calls, err)
		}
	}
}

func TestFileQueriesRejectInvalidDirectoryFlags(t *testing.T) {
	for _, operation := range []string{"meta", "batch", "search", "list"} {
		for _, contextual := range []bool{false, true} {
			for _, flags := range [][2]int{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {2, 0}, {-1, 0}, {127, 0}, {0, 2}, {1, -1}, {1, 127}} {
				t.Run(fmt.Sprintf("%s/context=%t/%d/%d", operation, contextual, flags[0], flags[1]), func(t *testing.T) {
					body := &locateBody{Reader: strings.NewReader(fmt.Sprintf(`{"errno":0,"error_code":0,"list":[{"fs_id":1,"path":"/one","size":0,"isdir":%d,"ifhassubdir":%d}]}`, flags[0], flags[1]))}
					pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
						resp := response(r, "")
						resp.Body = body
						return resp, nil
					})
					files, err := querySizeFixture(pcs, operation, contextual)
					invalid := flags[0] < 0 || flags[0] > 1 || flags[1] < 0 || flags[1] > 1
					if invalid {
						if err == nil || files != nil {
							t.Errorf("invalid directory flags accepted: count=%d err=%v", len(files), err)
						}
					} else if err != nil || len(files) != 1 || files[0].Isdir != (flags[0] == 1) || files[0].Ifhassubdir != (flags[1] == 1) {
						t.Errorf("valid directory flags changed: count=%d err=%v", len(files), err)
					}
					if !body.closed {
						t.Error("response body not closed")
					}
				})
			}
		}
	}
}

func TestFileQueryConversionPreservesMetadata(t *testing.T) {
	for _, operation := range []string{"meta", "batch", "search", "list"} {
		t.Run(operation, func(t *testing.T) {
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				return response(r, `{"errno":0,"error_code":0,"list":[{"fs_id":12345678901,"app_id":23456789012,"path":"/one","server_filename":"one","ctime":1700000001,"mtime":1700000002,"server_ctime":1700000001,"server_mtime":1700000002,"md5":"d41d8cd98f00b204e9800998ecf8427e","block_list":["first","second"],"size":4294967297,"isdir":0,"ifhassubdir":1}]}`), nil
			})
			files, err := querySizeFixture(pcs, operation, true)
			want := &FileDirectory{
				FsID: 12345678901, AppID: 23456789012,
				Path: "/one", Filename: "one", Ctime: 1700000001, Mtime: 1700000002,
				MD5: "d41d8cd98f00b204e9800998ecf8427e", Size: 4294967297,
				BlockListJSON: BlockListJSON{BlockList: []string{"first", "second"}},
				Ifhassubdir:   true,
			}
			if err != nil || len(files) != 1 || !reflect.DeepEqual(files[0], want) {
				t.Fatalf("metadata changed during conversion: files=%+v err=%v", files, err)
			}
		})
	}
}
