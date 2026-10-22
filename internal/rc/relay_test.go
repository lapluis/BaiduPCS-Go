package rc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type fakeCloud struct {
	files map[string][]byte
	id    int64
}
type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
func (c *fakeCloud) endpoint(uid uint64, root string) Endpoint {
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = fakeTransport(func(req *http.Request) (*http.Response, error) {
		if req.Body != nil {
			defer req.Body.Close()
		}
		var payload struct {
			List []struct {
				Path string `json:"path"`
			} `json:"list"`
		}
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			return nil, err
		}
		if req.MultipartForm != nil {
			defer req.MultipartForm.RemoveAll()
		}
		if err := json.Unmarshal([]byte(req.FormValue("param")), &payload); err != nil {
			return nil, err
		}
		if len(payload.List) != 1 {
			return nil, errors.New("unexpected fixture request")
		}
		p := payload.List[0].Path
		result := map[string]interface{}{}
		data, exists := c.files[p]
		switch req.URL.Query().Get("method") {
		case "meta":
			if p == "/Temp" {
				result["list"] = []interface{}{map[string]interface{}{"path": p, "isdir": 1, "fs_id": 1}}
			} else if !exists {
				result["error_code"] = 31066
			} else {
				result["list"] = []interface{}{map[string]interface{}{"path": p, "size": len(data), "mtime": 1, "fs_id": c.id}}
			}
		case "delete":
			if !exists {
				return nil, errors.New("deleting absent fixture")
			}
			delete(c.files, p)
		default:
			return nil, errors.New("unexpected API; cross-account relay must not use server copy")
		}
		b, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(b))), Request: req}, nil
	})
	return Endpoint{&Remote{UID: uid, PCS: baidupcs.NewPCSWithClient(1, client)}, root}
}
func TestCrossAccountRelayAndFailedMove(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "upload-failure"}[fail], func(t *testing.T) {
			s := &fakeCloud{map[string][]byte{"/Temp/a": []byte("relay-content")}, 101}
			d := &fakeCloud{map[string][]byte{}, 202}
			src, dst := s.endpoint(11, "/Temp/a"), d.endpoint(22, "/Temp/b")
			old := runTransferWorker
			defer func() { runTransferWorker = old }()
			var relay string
			runTransferWorker = func(ctx context.Context, kind string, uid uint64, from, to string, rate int64) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				cloud := s
				if uid == 22 {
					cloud = d
				} else if uid != 11 {
					return errors.New("wrong account")
				}
				if kind == "download" {
					data, ok := cloud.files[from]
					if !ok {
						return os.ErrNotExist
					}
					return os.WriteFile(to, data, 0600)
				}
				if uid != 22 {
					return errors.New("upload used source account")
				}
				relay = from
				if fail {
					return errors.New("injected upload failure")
				}
				data, err := os.ReadFile(from)
				if err != nil {
					return err
				}
				cloud.files[to] = data
				return nil
			}
			p, err := FilePlan(context.Background(), src, dst, true, opts())
			if err != nil {
				t.Fatal(err)
			}
			_, err = Execute(context.Background(), p, opts())
			if fail {
				if err == nil {
					t.Fatal("failure swallowed")
				}
				if _, ok := s.files["/Temp/a"]; !ok {
					t.Fatal("source deleted on failed relay")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if string(d.files["/Temp/b"]) != "relay-content" {
					t.Fatal("content lost")
				}
				if _, ok := s.files["/Temp/a"]; ok {
					t.Fatal("move did not delete source")
				}
			}
			if relay == "" {
				t.Fatal("relay path unused")
			}
			if _, err := os.Stat(path.Dir(strings.ReplaceAll(relay, `\`, "/"))); !os.IsNotExist(err) {
				t.Fatal("relay temporary directory not cleaned", err)
			}
		})
	}
}
