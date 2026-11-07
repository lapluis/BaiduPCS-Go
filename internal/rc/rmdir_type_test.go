package rc

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestRemoteRmdirRejectsFilesBeforeListing(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(fmt.Sprint(dry), func(t *testing.T) {
			calls, logs := 0, 0
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer r.Body.Close()
				}
				calls++
				payload := `{"errno":0,"list":[]}`
				if calls == 1 {
					payload = `{"error_code":0,"list":[{"path":"/file","size":3,"fs_id":7,"isdir":0}]}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: &emptyUploadResultBody{Reader: strings.NewReader(payload)}, Request: r}, nil
			})
			e := Endpoint{FS: &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}, Root: "/file"}
			o := opts()
			o.DryRun = dry
			o.Emit = func(string, string) { logs++ }
			err := Delete(context.Background(), e, "rmdir", o)
			if err == nil || !strings.Contains(err.Error(), "rmdir expects a directory") || calls != 1 || logs != 0 {
				t.Fatalf("file accepted or listed: calls=%d logs=%d err=%v", calls, logs, err)
			}
		})
	}
}

func TestRmdirTargetTypesAgreeForPreviewAndExecution(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "nonempty-directory"} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry=%t", kind, dry), func(t *testing.T) {
				root := filepath.Join(t.TempDir(), "target")
				if kind == "file" {
					if err := os.WriteFile(root, []byte("retain"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Mkdir(root, 0700); err != nil {
						t.Fatal(err)
					}
					if kind == "nonempty-directory" {
						if err := os.WriteFile(filepath.Join(root, "data"), []byte("retain"), 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				var events []string
				o := opts()
				o.DryRun = dry
				o.Emit = func(kind, _ string) { events = append(events, kind) }
				err := Delete(context.Background(), Endpoint{FS: &Local{}, Root: root}, "rmdir", o)
				if kind == "empty-directory" {
					if err != nil || len(events) != 1 {
						t.Fatal("empty directory rejected", events, err)
					}
					want := "rmdir"
					if dry {
						want = "would-rmdir"
					}
					if events[0] != want {
						t.Fatal(events)
					}
					_, statErr := os.Stat(root)
					if dry && statErr != nil || !dry && !os.IsNotExist(statErr) {
						t.Fatal("wrong directory mutation", statErr)
					}
				} else {
					if err == nil || len(events) != 0 {
						t.Fatal("invalid target reported success", events, err)
					}
					data := root
					if kind == "nonempty-directory" {
						data = filepath.Join(root, "data")
					}
					if b, err := os.ReadFile(data); err != nil || string(b) != "retain" {
						t.Fatal("invalid rmdir changed contents", err)
					}
				}
			})
		}
	}
}
