package pcscommand

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func TestCollectedTransferDownloadPaths(t *testing.T) {
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	var savedPath string
	client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
		switch req.URL.Path {
		case "/share/verify":
			body = `{"errno":0}`
		case "/share/list":
			body = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a.txt"},{"fs_id":2,"server_filename":"b.txt"}]}`
		case "/rest/2.0/pcs/file":
			body = `{}`
		case "/share/transfer":
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			savedPath = req.Form.Get("path")
			encoded, _ := json.Marshal(map[string]interface{}{"errno": 0, "info": []map[string]string{{"path": savedPath + "/a.txt"}, {"path": savedPath + "/b.txt"}}})
			body = string(encoded)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	got, err := ShareTransferTo(baidupcs.NewPCSWithClient(1, client), []string{"https://pan.baidu.com/s/1fixture", "abcd"}, "/saved", &baidupcs.TransferOption{Collect: true})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := "/saved/a.txt等文件"
	if savedPath != wantDir || got.Path != wantDir {
		t.Fatalf("wrong collect directory: %q", savedPath)
	}
	if !reflect.DeepEqual(got.downloadPaths(), []string{wantDir + "/a.txt", wantDir + "/b.txt"}) {
		t.Fatalf("downloads do not target collected files: %q", got.downloadPaths())
	}
}

type shareFixtureTransport func(*http.Request) (*http.Response, error)

func (f shareFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShareTransferPreservesCommaInFilename(t *testing.T) {
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = shareFixtureTransport(func(req *http.Request) (*http.Response, error) {
		body := `{"loginstate":1,"bdstoken":"synthetic","shareid":1,"share_uk":2}`
		switch req.URL.Path {
		case "/share/verify":
			if err := req.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if req.Form.Get("pwd") != "abcd" {
				t.Error("incorrect extraction code sent")
			}
			body = `{"errno":0,"randsk":"synthetic"}`
		case "/share/list":
			body = `{"errno":0,"list":[{"fs_id":1,"server_filename":"a,b.txt"},{"fs_id":2,"server_filename":"c.txt"}]}`
		case "/share/transfer":
			body = `{"errno":0,"info":[{"fsid":1,"path":"/saved/a,b.txt"},{"fsid":2,"path":"/saved/c.txt"}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})
	for _, params := range [][]string{
		{"https://pan.baidu.com/s/1fixture?from=share&pwd=abcd"},
		{"https://pan.baidu.com/s/1fixture?pwd=wrong", "abcd"},
		{"分享链接：https://pan.baidu.com/s/1fixture?from=share&pwd=abcd 提取码：abcd"},
		{"https://pan.baidu.com/share/init?pwd=abcd&surl=fixture"},
	} {
		got, err := ShareTransferTo(baidupcs.NewPCSWithClient(1, client), params, "/saved", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Filenames, []string{"a,b.txt", "c.txt"}) {
			t.Fatalf("filenames changed: %q", got.Filenames)
		}
		if !reflect.DeepEqual(got.downloadPaths(), []string{"/saved/a,b.txt", "/saved/c.txt"}) {
			t.Fatalf("ordinary transfer download paths changed: %q", got.downloadPaths())
		}
	}
}

// These cases fail before any network request, so a nil client is safe and the
// validation shared by the legacy command and pcsclone is covered.
func TestShareTransferValidation(t *testing.T) {
	cases := []struct {
		name   string
		params []string
		want   string
	}{
		{name: "empty", params: nil, want: "分享链接不能为空"},
		{name: "too many", params: []string{"a", "b", "c"}, want: "参数过多"},
		{name: "bad feature", params: []string{"https://pan.baidu.com/s/x", "abcd"}, want: "链接地址或提取码非法"},
		{name: "bad code", params: []string{"https://pan.baidu.com/s/1abcdefghijklmnopqrstuv", "abc"}, want: "链接地址或提取码非法"},
		{name: "foreign host", params: []string{"https://fixture.invalid/s/1fixture", "abcd"}, want: "链接地址或提取码非法"},
		{name: "host suffix", params: []string{"https://pan.baidu.com.fixture.invalid/s/1fixture", "abcd"}, want: "链接地址或提取码非法"},
		{name: "wrong path", params: []string{"https://pan.baidu.com/other/1fixture", "abcd"}, want: "链接地址或提取码非法"},
		{name: "extra path", params: []string{"https://pan.baidu.com/s/1fixture/extra", "abcd"}, want: "链接地址或提取码非法"},
		{name: "long code", params: []string{"https://pan.baidu.com/s/1fixture?pwd=abcde"}, want: "链接地址或提取码非法"},
		{name: "missing surl", params: []string{"https://pan.baidu.com/share/init?pwd=abcd"}, want: "链接地址或提取码非法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ShareTransferTo(nil, tc.params, "/", nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
