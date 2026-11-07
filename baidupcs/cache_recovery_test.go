package baidupcs

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDirectoryCacheRecoversAfterInvalidListing(t *testing.T) {
	for _, failure := range []string{"negative", "null", "missing-list", "duplicate", "later-negative"} {
		for _, contextual := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/context=%t", failure, contextual), func(t *testing.T) {
				calls := 0
				recovered := false
				var bodies []*locateBody
				pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
					calls++
					payload := `{"errno":0,"list":[{"fs_id":7,"path":"/Temp/one","size":8}]}`
					if !recovered {
						switch failure {
						case "negative":
							payload = `{"errno":0,"list":[{"fs_id":1,"path":"/Temp/bad","size":-1}]}`
						case "null":
							payload = `{"errno":0,"list":[null]}`
						case "missing-list":
							payload = `{"errno":0}`
						case "duplicate":
							payload = `{"errno":0,"list":[{"fs_id":1,"path":"/Temp/one"},{"fs_id":1,"path":"/Temp/two"}]}`
						case "later-negative":
							payload = pageBody(1, maxListNum)
							if calls > 1 {
								payload = `{"errno":0,"list":[{"fs_id":1001,"path":"/Temp/bad","size":-1}]}`
							}
						}
					}
					body := &locateBody{Reader: strings.NewReader(payload)}
					bodies = append(bodies, body)
					resp := response(req, "")
					resp.Body = body
					return resp, nil
				})
				fetch := func(withContext bool) (FileDirectoryList, error) {
					if withContext {
						files, err := pcs.CacheFilesDirectoriesListWithContext(context.Background(), "/Temp", nil)
						return files, err
					}
					files, err := pcs.CacheFilesDirectoriesList("/Temp", nil)
					return files, err
				}
				failedCalls := 1
				if failure == "later-negative" {
					failedCalls = 2
				}
				files, err := fetch(contextual)
				if err == nil || files != nil || calls != failedCalls {
					t.Fatalf("invalid listing published: count=%d calls=%d err=%v", len(files), calls, err)
				}
				recovered = true
				for i, withContext := range []bool{!contextual, contextual} {
					files, err = fetch(withContext)
					if err != nil || len(files) != 1 || files[0].FsID != 7 || files[0].Size != 8 || calls != failedCalls+1 {
						t.Fatalf("retry/cache hit %d failed: count=%d calls=%d err=%v", i, len(files), calls, err)
					}
				}
				for i, body := range bodies {
					if !body.closed {
						t.Errorf("response %d not closed", i)
					}
				}
			})
		}
	}
}
