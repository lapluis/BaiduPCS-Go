package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestDirectoryCacheReturnsIndependentResults(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			for _, mutation := range []string{"slice", "metadata", "blocks", "tree"} {
				t.Run(fmt.Sprintf("warm=%t/legacy=%t/%s", warm, legacy, mutation), func(t *testing.T) {
					calls := 0
					pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
						calls++
						return response(req, `{"errno":0,"list":[{"fs_id":1,"path":"/Temp/a","server_filename":"a","size":4,"md5":"fixture","block_list":["block-a","block-b"]},{"fs_id":2,"path":"/Temp/dir","server_filename":"dir","isdir":1}]}`), nil
					})
					query := func(client *BaiduPCS) (FileDirectoryList, error) {
						if legacy {
							return client.CacheFilesDirectoriesList("/Temp", nil)
						}
						return client.CacheFilesDirectoriesListWithContext(context.Background(), "/Temp", nil)
					}
					if warm {
						if _, err := query(pcs); err != nil {
							t.Fatal(err)
						}
					}
					files, err := query(pcs)
					if err != nil || len(files) != 2 {
						t.Fatalf("initial listing: entries=%d err=%v", len(files), err)
					}
					switch mutation {
					case "slice":
						files[0], files[1] = files[1], nil
					case "metadata":
						files[0].Path, files[0].Filename = "/changed", "changed"
						files[0].FsID, files[0].Size, files[0].MD5 = 99, 999, "changed"
					case "blocks":
						files[0].BlockList[0] = "changed"
					case "tree":
						files[0].PreBase = "changed"
						files[0].Parent = files[1]
						files[0].Children = FileDirectoryList{files[1]}
					}
					// CopyPCS intentionally shares the cache; it must not share
					// the mutable result objects returned to another caller.
					got, err := query(pcs.CopyPCS())
					want := FileDirectoryList{
						{FsID: 1, Path: "/Temp/a", Filename: "a", Size: 4, MD5: "fixture", BlockListJSON: BlockListJSON{BlockList: []string{"block-a", "block-b"}}},
						{FsID: 2, Path: "/Temp/dir", Filename: "dir", Isdir: true},
					}
					if err != nil || !reflect.DeepEqual(got, want) || calls != 1 {
						t.Fatalf("caller mutation changed cached listing: entries=%#v calls=%d err=%v", got, calls, err)
					}
					if got[0] == files[0] || got[1] == files[1] && files[1] != nil {
						t.Fatal("cached result entries still alias")
					}
				})
			}
		}
	}
}

func TestDirectoryCacheEmptyResultRemainsNil(t *testing.T) {
	calls := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		calls++
		return response(req, `{"errno":0,"list":[]}`), nil
	})
	for i := 0; i < 2; i++ {
		got, err := pcs.CacheFilesDirectoriesList("/Temp", nil)
		if err != nil || got != nil {
			t.Fatalf("empty listing changed: entries=%#v err=%v", got, err)
		}
	}
	if calls != 1 {
		t.Fatalf("empty listing not cached: calls=%d", calls)
	}
}
