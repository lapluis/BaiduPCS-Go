package baidupcs

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestRapidUploadContextPreservesTargetChecksAndHandshake(t *testing.T) {
	for _, directory := range []bool{false, true} {
		calls := []string{}
		pcs := fixturePCS(func(r *http.Request) (*http.Response, error) {
			if r.URL.Query().Get("method") == "meta" {
				calls = append(calls, "meta")
				if directory {
					return response(r, `{"list":[{"path":"/fixture","isdir":1}]}`), nil
				}
				return response(r, `{"error_code":31066}`), nil
			}
			calls = append(calls, "precreate")
			if r.URL.Path != "/api/precreate" {
				t.Errorf("unexpected request path: %s", r.URL.Path)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("uploadid") != "session" || r.Form.Get("content-md5") != EmptyContentMD5 || r.Form.Get("slice-md5") != EmptyContentMD5 || r.Form.Get("size") != "8" || r.Form.Get("path") != "/fixture" {
				t.Error("context wrapper changed handshake parameters")
			}
			return response(r, `{"errno":0,"return_type":1,"uploadid":"session"}`), nil
		})
		err, info := pcs.RapidUploadWithContext(context.Background(), "/fixture", OverWritePolicy, "session", strings.ToUpper(EmptyContentMD5), strings.ToUpper(EmptyContentMD5), "", "0", 0, 8, 8, 1, []string{EmptyContentMD5})
		if directory {
			if err == nil || len(calls) != 1 || calls[0] != "meta" {
				t.Fatalf("directory target reached precreation: calls=%v err=%v", calls, err)
			}
		} else if err != nil || info.UploadID != "session" || info.ReturnType != 1 || len(calls) != 2 || calls[0] != "meta" || calls[1] != "precreate" {
			t.Fatalf("normal rapid handshake failed: calls=%v info=%+v err=%v", calls, info, err)
		}
	}
}
