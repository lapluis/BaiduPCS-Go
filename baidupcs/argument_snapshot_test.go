package baidupcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func decodeSnapshotParam(t *testing.T, req *http.Request, data interface{}) {
	t.Helper()
	defer req.Body.Close()
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(req.FormValue("param")), data); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataValidationUsesSentPathSnapshot(t *testing.T) {
	for _, mode := range []string{"matching", "replacement", "duplicate"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", mode, contextual), func(t *testing.T) {
				paths := []string{"/wanted", "/other"}
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					var sent PathsListJSON
					decodeSnapshotParam(t, req, &sent)
					if len(sent.List) != 2 || sent.List[0].Path != "/wanted" || sent.List[1].Path != "/other" {
						t.Fatalf("wrong request paths: %+v", sent.List)
					}
					paths[0] = "/replacement"
					entry := `{"fs_id":1,"path":"/wanted","size":1}`
					if mode == "replacement" {
						entry = `{"fs_id":1,"path":"/replacement","size":1}`
					} else if mode == "duplicate" {
						paths[0], paths[1] = "/wanted", "/wanted"
						entry += `,{"fs_id":2,"path":"/wanted","size":1}`
					}
					return response(req, `{"error_code":0,"list":[`+entry+`]}`), nil
				})
				var files FileDirectoryList
				var err pcserror.Error
				if contextual {
					files, err = pcs.FilesDirectoriesBatchMetaWithContext(context.Background(), paths...)
				} else {
					files, err = pcs.FilesDirectoriesBatchMeta(paths...)
				}
				if mode == "matching" {
					if err != nil || len(files) != 1 || files[0].Path != "/wanted" {
						t.Fatalf("sent path no longer accepted: count=%d err=%v", len(files), err)
					}
				} else if err == nil || files != nil {
					t.Fatalf("unsent or excess metadata accepted: count=%d err=%v", len(files), err)
				}
				if mode != "duplicate" && paths[0] != "/replacement" || mode == "duplicate" && paths[1] != "/wanted" {
					t.Fatal("operation overwrote the caller's reused arguments")
				}
			})
		}
	}
}

func TestMutationInvalidationUsesSentPathSnapshot(t *testing.T) {
	for _, action := range []string{"remove", "copy-entry", "copy-slice", "copy-nil", "move-entry", "move-slice", "move-nil"} {
		for _, phase := range []string{"success", "lost", "canceled"} {
			for _, contextual := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/context=%t", action, phase, contextual), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					paths := []string{"/old"}
					entry := &CpMvJSON{From: "/old", To: "/new"}
					entries := []*CpMvJSON{entry}
					changed, requests := false, 0
					lists := make(map[string]int)
					lost := errors.New("synthetic response lost after commit")
					pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
						if req.URL.Path == "/api/list" {
							dir := req.URL.Query().Get("dir")
							lists[dir]++
							if changed {
								return response(req, fmt.Sprintf(`{"errno":0,"list":[{"fs_id":7,"path":%q,"size":1}]}`, path.Join(dir, "fresh"))), nil
							}
							return response(req, `{"errno":0,"list":[]}`), nil
						}
						requests++
						if action == "remove" {
							var sent PathsListJSON
							decodeSnapshotParam(t, req, &sent)
							if len(sent.List) != 1 || sent.List[0].Path != "/old" {
								t.Fatal("wrong delete request")
							}
							paths[0] = "/other"
						} else {
							var sent CpMvListJSON
							decodeSnapshotParam(t, req, &sent)
							if len(sent.List) != 1 || *sent.List[0] != (CpMvJSON{From: "/old", To: "/new"}) {
								t.Fatal("wrong copy/move request")
							}
							if strings.HasSuffix(action, "entry") {
								entry.From, entry.To = "/other", "/other-target"
							} else if strings.HasSuffix(action, "slice") {
								entries[0] = &CpMvJSON{From: "/other", To: "/other-target"}
							} else {
								entries[0] = nil
							}
						}
						changed = true
						if phase == "lost" {
							return nil, lost
						}
						if phase == "canceled" {
							cancel()
							return nil, context.Canceled
						}
						return response(req, `{"error_code":0}`), nil
					})
					dirs := []string{"/", "/old", "/old/sub", "/new", "/new/sub", "/other", "/other/sub", "/other-target", "/safe"}
					for _, dir := range dirs {
						if _, err := pcs.CacheFilesDirectoriesList(dir, nil); err != nil {
							t.Fatal(err)
						}
					}
					err := invokeSnapshotMutation(pcs, ctx, action, contextual, paths, entries)
					if requests != 1 || (err == nil) != (phase == "success") || phase == "lost" && !errors.Is(err, lost) || phase == "canceled" && !errors.Is(err, context.Canceled) {
						t.Fatalf("mutation result changed: requests=%d err=%v", requests, err)
					}
					clone := pcs.CopyPCS()
					for _, dir := range dirs {
						affected := dir == "/" || !strings.HasPrefix(action, "copy") && (dir == "/old" || dir == "/old/sub") || action != "remove" && (dir == "/new" || dir == "/new/sub")
						wantCount, wantCalls := 0, 1
						if affected {
							wantCount, wantCalls = 1, 2
						}
						files, err := clone.CacheFilesDirectoriesList(dir, nil)
						if err != nil || len(files) != wantCount || lists[dir] != wantCalls {
							t.Errorf("wrong cache invalidation for %s: count=%d want=%d calls=%d want=%d err=%v", dir, len(files), wantCount, lists[dir], wantCalls, err)
						}
					}
				})
			}
		}
	}
}

func invokeSnapshotMutation(pcs *BaiduPCS, ctx context.Context, action string, contextual bool, paths []string, entries []*CpMvJSON) error {
	if action == "remove" {
		if contextual {
			return pcs.RemoveWithContext(ctx, paths...)
		}
		return pcs.Remove(paths...)
	}
	if strings.HasPrefix(action, "copy") {
		if contextual {
			return pcs.CopyWithContext(ctx, entries...)
		}
		return pcs.Copy(entries...)
	}
	if contextual {
		return pcs.MoveWithContext(ctx, entries...)
	}
	return pcs.Move(entries...)
}
