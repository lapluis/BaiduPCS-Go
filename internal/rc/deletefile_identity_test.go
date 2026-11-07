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
	"time"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type deletefileChangingLocal struct {
	Local
	change  func(string, Entry) error
	changed bool
	removes int
}

func TestRemoteDeletefileRejectsChangedMetadataBeforeDeleteRequest(t *testing.T) {
	for _, mode := range []string{"identity", "size", "mtime"} {
		t.Run(mode, func(t *testing.T) {
			metadata, removals, logs := 0, 0, 0
			client := requester.NewHTTPClient()
			client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
				if req.Body != nil {
					defer req.Body.Close()
				}
				payload := `{"error_code":0}`
				if req.URL.Query().Get("method") == "meta" {
					metadata++
					id, size, mtime := 7, 8, 100
					if metadata > 1 {
						switch mode {
						case "identity":
							id++
						case "size":
							size++
						case "mtime":
							mtime++
						}
					}
					payload = fmt.Sprintf(`{"error_code":0,"list":[{"path":"/target","size":%d,"fs_id":%d,"mtime":%d,"isdir":0}]}`, size, id, mtime)
				} else {
					removals++
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})
			e := Endpoint{FS: &Remote{UID: 7, PCS: baidupcs.NewPCSWithClient(7, client)}, Root: "/target"}
			o := opts()
			o.Emit = func(string, string) { logs++ }
			err := Delete(context.Background(), e, "deletefile", o)
			if err == nil || !strings.Contains(err.Error(), "changed after planning") || metadata != 2 || removals != 0 || logs != 0 {
				t.Fatalf("changed remote file reached deletion: metadata=%d removals=%d logs=%d err=%v", metadata, removals, logs, err)
			}
		})
	}
}

func (l *deletefileChangingLocal) Stat(ctx context.Context, p string) (Entry, error) {
	entry, err := l.Local.Stat(ctx, p)
	if err == nil && !l.changed {
		l.changed = true
		if err := l.change(p, entry); err != nil {
			return Entry{}, err
		}
	}
	return entry, err
}

func (l *deletefileChangingLocal) Remove(ctx context.Context, p string, dir bool) error {
	l.removes++
	return l.Local.Remove(ctx, p, dir)
}

func TestDeletefileRechecksSelectedFileBeforeRemoval(t *testing.T) {
	for _, mode := range []string{"size", "mtime", "replacement", "missing", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "target")
			if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			fs := &deletefileChangingLocal{change: func(p string, entry Entry) error {
				switch mode {
				case "size":
					return os.WriteFile(p, []byte("external modification"), 0600)
				case "mtime":
					stamp := entry.ModTime.Add(time.Hour)
					return os.Chtimes(p, stamp, stamp)
				case "replacement":
					if err := os.Rename(p, p+".original"); err != nil {
						return err
					}
					if err := os.WriteFile(p, []byte("replaced"), 0600); err != nil {
						return err
					}
					return os.Chtimes(p, entry.ModTime, entry.ModTime)
				case "missing":
					return os.Remove(p)
				default:
					return nil
				}
			}}
			events := 0
			o := opts()
			o.Emit = func(string, string) { events++ }
			err := Delete(context.Background(), Endpoint{FS: fs, Root: target}, "deletefile", o)
			if mode == "unchanged" {
				if err != nil || fs.removes != 1 || events != 1 {
					t.Fatalf("unchanged file was not deleted: removes=%d events=%d err=%v", fs.removes, events, err)
				}
				if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unchanged file remains: %v", err)
				}
				return
			}
			if err == nil || fs.removes != 0 || events != 0 {
				t.Fatalf("changed file reached removal: removes=%d events=%d err=%v", fs.removes, events, err)
			}
			if mode == "missing" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing file cause lost: %v", err)
				}
				return
			}
			if !strings.Contains(err.Error(), "changed after planning") {
				t.Fatalf("changed file cause lost: %v", err)
			}
			data, readErr := os.ReadFile(target)
			want := "original"
			if mode == "replacement" {
				want = "replaced"
			} else if mode == "size" {
				want = "external modification"
			}
			if readErr != nil || string(data) != want {
				t.Fatalf("changed file lost: data=%q err=%v", data, readErr)
			}
		})
	}
}
