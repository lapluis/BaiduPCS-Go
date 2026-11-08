package baidupcs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestShareTransferInvalidatesDestinationCache(t *testing.T) {
	for _, root := range []bool{false, true} {
		for _, relative := range []bool{false, true} {
			for _, phase := range []string{"success", "lost", "canceled", "malformed", "remote-error"} {
				for _, contextual := range []bool{false, true} {
					t.Run(fmt.Sprintf("root=%t/relative=%t/%s/context=%t", root, relative, phase, contextual), func(t *testing.T) {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						changed, mutations, lists := false, 0, 0
						target := "saved"
						if relative {
							target = "/saved"
						}
						if root {
							target = ""
						}
						params := map[string]string{"shareUrl": "https://pan.baidu.com/share/transfer", "path": target, "fs_id": "[1]"}
						pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
							if req.Body != nil {
								defer req.Body.Close()
							}
							if req.URL.Path == "/api/list" {
								lists++
								if changed {
									child := path.Join(req.URL.Query().Get("dir"), "fresh")
									return response(req, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":7,"path":%q,"size":1}]}`, child)), nil
								}
								return response(req, `{"errno":0,"list":[]}`), nil
							}
							if req.URL.Path != "/share/transfer" || req.Method != http.MethodPost {
								t.Fatalf("unexpected request: %s %s", req.Method, req.URL.Path)
							}
							if err := req.ParseForm(); err != nil {
								t.Fatal(err)
							}
							if req.Form.Get("path") != target {
								t.Fatalf("wrong submitted destination: %q", req.Form.Get("path"))
							}
							mutations++
							changed = true
							// The caller may reuse its map after request encoding.
							params["path"] = "/other"
							switch phase {
							case "lost":
								return nil, errors.New("synthetic response lost after commit")
							case "canceled":
								cancel()
								return nil, context.Canceled
							case "malformed":
								return response(req, `not-json`), nil
							case "remote-error":
								return response(req, `{"errno":12}`), nil
							}
							return response(req, fmt.Sprintf(`{"errno":0,"info":[{"path":%q}]}`, path.Join("/"+target, "fresh"))), nil
						})
						query := func(dir string) string {
							if relative {
								return path.Clean(dir)
							}
							return "/" + dir
						}
						dirs := []string{"", "saved", "saved/sub", "saved-other", "other"}
						orders := []*OrderOptions{DefaultOrderOptions, {By: OrderByTime, Order: OrderDesc}}
						for _, dir := range dirs {
							for _, order := range orders {
								if _, err := pcs.CacheFilesDirectoriesList(query(dir), order); err != nil {
									t.Fatal(err)
								}
							}
						}
						var result map[string]string
						if contextual {
							result = pcs.GenerateRequestQueryWithContext(ctx, http.MethodPost, params)
						} else {
							result = pcs.GenerateRequestQuery(http.MethodPost, params)
						}
						if (result["ErrNo"] == "0") != (phase == "success") || mutations != 1 {
							t.Fatalf("mutation result changed: result=%v calls=%d", result, mutations)
						}
						client := pcs.CopyPCS()
						for _, dir := range dirs {
							affected := root || dir == "saved" || dir == "saved/sub"
							for _, order := range orders {
								before := lists
								for repeat := 0; repeat < 2; repeat++ {
									files, err := client.CacheFilesDirectoriesList(query(dir), order)
									want := 0
									if affected {
										want = 1
									}
									if err != nil || len(files) != want {
										t.Errorf("stale listing %q: count=%d want=%d err=%v", query(dir), len(files), want, err)
									}
								}
								if (lists == before+1) != affected || !affected && lists != before {
									t.Errorf("wrong refresh scope %q: requests=%d affected=%t", query(dir), lists-before, affected)
								}
							}
						}
						if params["path"] != "/other" {
							t.Fatal("operation overwrote caller's reused parameters")
						}
					})
				}
			}
		}
	}
}

func TestShareTransferInvalidatesInflightListing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	var lists atomic.Int32
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			defer req.Body.Close()
		}
		if req.URL.Path == "/api/list" {
			if lists.Add(1) == 1 {
				close(started)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				return response(req, `{"errno":0,"list":[]}`), nil
			}
			return response(req, `{"errno":0,"list":[{"fs_id":7,"path":"/saved/sub/fresh","size":1}]}`), nil
		}
		return response(req, `{"errno":0,"info":[{"path":"/saved/sub"}]}`), nil
	})
	done := make(chan error, 1)
	go func() {
		_, err := pcs.CacheFilesDirectoriesListWithContext(ctx, "/saved/sub", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("listing did not start")
	}
	result := pcs.GenerateRequestQueryWithContext(ctx, http.MethodPost, map[string]string{
		"shareUrl": "https://pan.baidu.com/share/transfer", "path": "/saved", "fs_id": "[1]",
	})
	unblock()
	if result["ErrNo"] != "0" {
		t.Fatal(result)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("listing did not finish")
	}
	client := pcs.CopyPCS()
	for repeat := 0; repeat < 2; repeat++ {
		files, err := client.CacheFilesDirectoriesListWithContext(ctx, "/saved/sub", nil)
		if err != nil || len(files) != 1 || files[0].Path != "/saved/sub/fresh" {
			t.Fatalf("pre-transfer listing became cached: files=%v err=%v", files, err)
		}
	}
	if lists.Load() != 2 {
		t.Fatalf("fresh listing was not cached: calls=%d want=2", lists.Load())
	}
}

func TestShareReadQueryPreservesDirectoryCache(t *testing.T) {
	lists := 0
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			defer req.Body.Close()
		}
		if req.URL.Path == "/api/list" {
			lists++
			return response(req, `{"errno":0,"list":[]}`), nil
		}
		if req.Method != http.MethodGet {
			t.Fatalf("unexpected query method: %s", req.Method)
		}
		return response(req, `{"errno":0,"info":[{"path":"/saved/fresh"}]}`), nil
	})
	if _, err := pcs.CacheFilesDirectoriesList("/saved", nil); err != nil {
		t.Fatal(err)
	}
	result := pcs.GenerateRequestQuery(http.MethodGet, map[string]string{
		"shareUrl": "https://pan.baidu.com/share/transfer", "path": "/saved",
	})
	if result["ErrNo"] != "0" {
		t.Fatal(result)
	}
	if _, err := pcs.CacheFilesDirectoriesList("/saved", nil); err != nil || lists != 1 {
		t.Fatalf("read query discarded a healthy cache: calls=%d err=%v", lists, err)
	}
}
