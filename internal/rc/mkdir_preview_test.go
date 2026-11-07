package rc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestLegacyMkdirPreviewRejectsOccupiedPaths(t *testing.T) {
	for _, ancestor := range []bool{false, true} {
		for _, dry := range []bool{false, true} {
			t.Run(fmt.Sprintf("ancestor=%t/dry=%t", ancestor, dry), func(t *testing.T) {
				root := t.TempDir()
				blocked := filepath.Join(root, "file")
				if err := os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				target := blocked
				if ancestor {
					target = filepath.Join(blocked, "missing", "child")
				}
				args := []string{target}
				if dry {
					args = append(args, "--dry-run")
				}
				p, err := parse(args)
				if err != nil {
					t.Fatal(err)
				}
				events := 0
				p.Emit = func(string, string) { events++ }
				err = runCommand(context.Background(), "mkdir", p, &Resolver{CWD: root}, io.Discard)
				if err == nil || events != 0 {
					t.Errorf("occupied target accepted: events=%d err=%v", events, err)
				}
				if data, err := os.ReadFile(blocked); err != nil || string(data) != "preserve" {
					t.Fatalf("occupied file changed: data=%q err=%v", data, err)
				}
			})
		}
	}
}

func TestRemoteMkdirPreviewChecksTypesWithoutCreating(t *testing.T) {
	for _, mode := range []string{"target-file", "ancestor-file", "missing", "directory", "error"} {
		t.Run(mode, func(t *testing.T) {
			metadata, mutations := 0, 0
			cause := errors.New("fixture metadata unavailable")
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					defer req.Body.Close()
				}
				if req.URL.Query().Get("method") != "meta" {
					mutations++
					return nil, errors.New("unexpected mutation request")
				}
				metadata++
				if mode == "error" {
					return nil, cause
				}
				payload := `{"error_code":31066}`
				if mode == "target-file" || mode == "directory" {
					isDir := 0
					if mode == "directory" {
						isDir = 1
					}
					payload = fmt.Sprintf(`{"error_code":0,"list":[{"path":"/parent/child","fs_id":7,"isdir":%d}]}`, isDir)
				} else if mode == "ancestor-file" && metadata == 2 {
					payload = `{"error_code":0,"list":[{"path":"/parent","fs_id":7,"isdir":0}]}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			e := Endpoint{FS: &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}, Root: "/parent/child"}
			err := ValidateMkdirTarget(context.Background(), e)
			wantMetadata := 1
			if mode == "ancestor-file" || mode == "missing" {
				wantMetadata = 2
			}
			if metadata != wantMetadata || mutations != 0 {
				t.Errorf("preview request mismatch: metadata=%d mutations=%d err=%v", metadata, mutations, err)
			}
			if mode == "target-file" || mode == "ancestor-file" {
				if err == nil || !strings.Contains(err.Error(), "file occupies directory") {
					t.Errorf("file accepted as directory: %v", err)
				}
			} else if mode == "error" {
				if !errors.Is(err, cause) {
					t.Errorf("metadata error hidden: %v", err)
				}
			} else if err != nil {
				t.Errorf("creatable directory rejected: %v", err)
			}
		})
	}
}
