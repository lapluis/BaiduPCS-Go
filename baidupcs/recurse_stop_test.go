package baidupcs

import (
	"net/http"
	"slices"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs/pcserror"
)

func TestRecursiveListingHonorsCallbackStop(t *testing.T) {
	for _, stop := range []string{"root", "directory", "file", "continue"} {
		t.Run(stop, func(t *testing.T) {
			var requests, visited []string
			pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
				if r.URL.Query().Get("method") == "meta" {
					requests = append(requests, "meta")
					return response(r, `{"error_code":0,"list":[{"fs_id":1,"path":"/Temp","isdir":1}]}`), nil
				}
				dir := r.URL.Query().Get("dir")
				requests = append(requests, dir)
				if dir == "/Temp" {
					return response(r, `{"errno":0,"list":[{"fs_id":2,"path":"/Temp/sub","isdir":1},{"fs_id":3,"path":"/Temp/last","size":1}]}`), nil
				}
				if dir == "/Temp/sub" {
					return response(r, `{"errno":0,"list":[{"fs_id":4,"path":"/Temp/sub/file","size":1}]}`), nil
				}
				t.Error("unexpected request", r.URL)
				return response(r, `{"errno":0,"list":[]}`), nil
			})
			pcs.FilesDirectoriesRecurseList("/Temp", nil, func(depth int, path string, file *FileDirectory, err pcserror.Error) bool {
				if err != nil || file == nil {
					t.Fatal("listing failed", err)
				}
				visited = append(visited, path)
				return !((stop == "root" && depth == 0) || (stop == "directory" && path == "/Temp/sub") || (stop == "file" && path == "/Temp/sub/file"))
			})
			wantRequests := []string{"meta", "/Temp", "/Temp/sub"}
			wantVisited := []string{"/Temp", "/Temp/sub", "/Temp/sub/file", "/Temp/last"}
			switch stop {
			case "root":
				wantRequests, wantVisited = wantRequests[:1], wantVisited[:1]
			case "directory":
				wantRequests, wantVisited = wantRequests[:2], wantVisited[:2]
			case "file":
				wantVisited = wantVisited[:3]
			}
			if !slices.Equal(requests, wantRequests) || !slices.Equal(visited, wantVisited) {
				t.Fatalf("callback stop ignored: requests=%v want=%v visited=%v want=%v", requests, wantRequests, visited, wantVisited)
			}
		})
	}
}
