package baidupcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestShareListValidatesEveryFilename(t *testing.T) {
	for _, tc := range []struct {
		name  interface{}
		valid bool
	}{
		{"../../escape", false}, {"nested/file", false}, {"/absolute", false},
		{".", false}, {"..", false}, {12, false},
		{"日本語, file.txt", true}, {"...", true}, {".hidden", true},
	} {
		for _, index := range []int{0, 1} {
			for _, withContext := range []bool{false, true} {
				t.Run(fmt.Sprintf("%v/index=%d/context=%t", tc.name, index, withContext), func(t *testing.T) {
					items := []map[string]interface{}{
						{"fs_id": 1, "server_filename": "first.txt"},
						{"fs_id": 2, "server_filename": "second.txt"},
					}
					items[index]["server_filename"] = tc.name
					payload, err := json.Marshal(map[string]interface{}{"errno": 0, "list": items})
					if err != nil {
						t.Fatal(err)
					}
					pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
						return response(req, string(payload)), nil
					})
					var got map[string]string
					if withContext {
						got = pcs.ExtractShareInfoWithContext(context.Background(), "https://pan.baidu.com/share/list", "1", "2", "synthetic")
					} else {
						got = pcs.ExtractShareInfo("https://pan.baidu.com/share/list", "1", "2", "synthetic")
					}
					if (got["ErrMsg"] == "success") != tc.valid {
						t.Fatalf("filename acceptance mismatch: %v", got)
					}
					if tc.valid {
						if got["fs_id"] != "[1,2]" || got["item_num"] != "2" || index == 0 && got["filename"] != tc.name {
							t.Fatalf("healthy metadata changed: %v", got)
						}
					} else if got["shareUrl"] != "" || got["fs_id"] != "" {
						t.Fatal("invalid metadata produced a transfer request")
					}
				})
			}
		}
	}
}
