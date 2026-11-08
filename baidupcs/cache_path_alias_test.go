package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"testing"
)

func TestDirectoryCacheInvalidatesRootedPathAliases(t *testing.T) {
	for _, operation := range []string{"mkdir", "remove", "copy", "move", "rename"} {
		for _, relativeQuery := range []bool{false, true} {
			for _, contextual := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/relative-query=%t/context=%t", operation, relativeQuery, contextual), func(t *testing.T) {
					changed, mutations, lists := false, 0, 0
					pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
						if req.URL.Path != "/api/list" {
							changed = true
							mutations++
							return response(req, `{"errno":0,"list":[]}`), nil
						}
						lists++
						if !changed {
							return response(req, `{"errno":0,"list":[]}`), nil
						}
						child := path.Join(req.URL.Query().Get("dir"), "item")
						return response(req, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":7,"path":%q,"size":1}]}`, child)), nil
					})
					ctx := context.Background()
					query := func(dir string) string {
						if relativeQuery {
							return path.Clean(dir)
						}
						return "/" + dir
					}
					mutationPath := func(dir string) string {
						if relativeQuery {
							return "/" + dir
						}
						return dir
					}
					client := pcs
					list := func(dir string, order *OrderOptions) (FileDirectoryList, error) {
						if contextual {
							return client.CacheFilesDirectoriesListWithContext(ctx, query(dir), order)
						}
						return client.CacheFilesDirectoriesList(query(dir), order)
					}
					dirs := []string{"", "dir", "dir/sub", "new", "new/sub", "dir-other", "other"}
					orders := []*OrderOptions{DefaultOrderOptions, {By: OrderByTime, Order: OrderDesc}}
					for _, dir := range dirs {
						for _, order := range orders {
							if _, err := list(dir, order); err != nil {
								t.Fatal(err)
							}
						}
					}
					var err error
					from, to := mutationPath("dir"), mutationPath("new")
					if contextual {
						switch operation {
						case "mkdir":
							err = pcs.MkdirWithContext(ctx, from+"/child")
						case "remove":
							err = pcs.RemoveWithContext(ctx, from)
						case "copy":
							err = pcs.CopyWithContext(ctx, &CpMvJSON{From: from, To: to})
						case "move":
							err = pcs.MoveWithContext(ctx, &CpMvJSON{From: from, To: to})
						case "rename":
							err = pcs.RenameWithContext(ctx, from, to)
						}
					} else {
						switch operation {
						case "mkdir":
							err = pcs.Mkdir(from + "/child")
						case "remove":
							err = pcs.Remove(from)
						case "copy":
							err = pcs.Copy(&CpMvJSON{From: from, To: to})
						case "move":
							err = pcs.Move(&CpMvJSON{From: from, To: to})
						case "rename":
							err = pcs.Rename(from, to)
						}
					}
					if err != nil || mutations != 1 {
						t.Fatalf("mutation failed: calls=%d err=%v", mutations, err)
					}
					client = pcs.CopyPCS()
					wantLists := len(dirs) * len(orders)
					for _, dir := range dirs {
						affected := dir == "dir" && operation == "mkdir"
						if operation != "mkdir" {
							affected = dir == "" || operation != "copy" && (dir == "dir" || dir == "dir/sub") || operation != "remove" && (dir == "new" || dir == "new/sub")
						}
						wantCount := 0
						if affected {
							wantCount = 1
							wantLists += len(orders)
						}
						for _, order := range orders {
							for repeat := 0; repeat < 2; repeat++ {
								files, err := list(dir, order)
								if err != nil || len(files) != wantCount {
									t.Errorf("stale cache %q: count=%d want=%d err=%v", query(dir), len(files), wantCount, err)
								} else if affected && files[0].Path != path.Join(query(dir), "item") {
									t.Errorf("request or response path spelling changed: %q", files[0].Path)
								}
							}
						}
					}
					if lists != wantLists {
						t.Errorf("cache request count=%d want=%d", lists, wantLists)
					}
				})
			}
		}
	}
}
