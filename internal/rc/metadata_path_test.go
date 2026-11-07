package rc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func metadataPathFixture(t *testing.T, returned string) (*Remote, *int) {
	t.Helper()
	calls := new(int)
	client := requester.NewHTTPClient()
	client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Body != nil {
			defer r.Body.Close()
		}
		*calls = *calls + 1
		payload := `{"error_code":0}`
		if r.URL.Query().Get("method") == "meta" {
			b, _ := json.Marshal(map[string]interface{}{"error_code": 0, "list": []interface{}{map[string]interface{}{"path": returned, "fs_id": 1, "size": 8}}})
			payload = string(b)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &emptyUploadResultBody{Reader: strings.NewReader(payload)}, Request: r}, nil
	})
	return &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}, calls
}

func TestWrongMetadataPathCannotMeanMissing(t *testing.T) {
	for _, returned := range []string{"/other", "", "fixture", "/fixture/child"} {
		t.Run(fmt.Sprintf("%q", returned), func(t *testing.T) {
			r, _ := metadataPathFixture(t, returned)
			_, err := r.Stat(context.Background(), "/fixture")
			if err == nil || os.IsNotExist(err) {
				t.Error("wrong response treated as absence", err)
			}
			info, err := workerDownloadInfo(context.Background(), r.PCS, "/fixture")
			if err == nil || info != nil {
				t.Fatal("worker accepted metadata for another path", info, err)
			}
		})
	}
}

func TestMatchingRemoteMetadataPathsRemainUsable(t *testing.T) {
	for _, tc := range []struct{ requested, returned string }{
		{"/fixture", "/fixture"}, {"/parent/../fixture", "/fixture"}, {"/fixture", "//fixture"},
	} {
		t.Run(tc.requested+"/"+tc.returned, func(t *testing.T) {
			r, _ := metadataPathFixture(t, tc.returned)
			e, err := r.Stat(context.Background(), tc.requested)
			if err != nil || e.Name != "fixture" || e.Size != 8 {
				t.Fatal("valid metadata rejected", e, err)
			}
			info, err := workerDownloadInfo(context.Background(), r.PCS, tc.requested)
			if err != nil || info == nil || info.Size != 8 {
				t.Fatal("worker rejected valid metadata", info, err)
			}
		})
	}
	if err := validateRemoteMetadataPath("/fixture", nil); err == nil || os.IsNotExist(err) {
		t.Fatal("missing metadata treated as absence", err)
	}
}

func TestWrongMetadataBlocksMkdirAndFilePlan(t *testing.T) {
	for _, mode := range []string{"mkdir", "copyto", "moveto"} {
		t.Run(mode, func(t *testing.T) {
			r, calls := metadataPathFixture(t, "/unrelated")
			var err error
			if mode == "mkdir" {
				err = r.Mkdir(context.Background(), "/fixture")
			} else {
				s, _ := endpoints(t)
				write(t, s, "source", "fixture")
				_, err = FilePlan(context.Background(), Endpoint{FS: s.FS, Root: s.At("source")}, Endpoint{FS: r, Root: "/fixture"}, mode == "moveto", opts())
			}
			if err == nil || os.IsNotExist(err) || *calls != 1 {
				t.Fatalf("wrong metadata authorized operation: calls=%d err=%v", *calls, err)
			}
		})
	}
}
