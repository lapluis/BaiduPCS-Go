package baidupcs

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type shareReadFailure struct{}

func (shareReadFailure) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }

type shareTrackedBody struct {
	io.Reader
	closed bool
}

func (b *shareTrackedBody) Close() error { b.closed = true; return nil }

func TestShareQueriesHandleReadFailure(t *testing.T) {
	for _, query := range []func(*BaiduPCS) bool{
		func(p *BaiduPCS) bool {
			return p.PostShareQuery("https://pan.baidu.com/share/verify", "", nil)["ErrMsg"] == "0"
		},
		func(p *BaiduPCS) bool {
			return p.ExtractShareInfo("https://pan.baidu.com/share/list", "1", "2", "token")["ErrMsg"] == "success"
		},
		func(p *BaiduPCS) bool {
			return p.GenerateRequestQuery("POST", map[string]string{"shareUrl": "https://pan.baidu.com/share/transfer"})["ErrNo"] == "0"
		},
	} {
		body := &shareTrackedBody{Reader: io.MultiReader(strings.NewReader(`{"errno":0,"list":[{"fs_id":1,"server_filename":"a"}]}`), shareReadFailure{})}
		pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { r := response(req, ""); r.Body = body; return r, nil })
		if query(pcs) {
			t.Error("read failure reported as success")
		}
		if !body.closed {
			t.Error("response body was not closed")
		}
	}
}

func TestShareQueriesRejectMalformedResponses(t *testing.T) {
	for _, body := range []string{"", "<html>gateway error</html>", `{}`, `{"errno":null}`, `{"errno":"bad"}`, `{"errno":0.5}`, `{"errno":0,`} {
		pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, body), nil })
		if got := pcs.PostShareQuery("https://pan.baidu.com/share/verify", "", nil); got["ErrMsg"] == "0" {
			t.Errorf("verify accepted %q", body)
		}
		if got := pcs.ExtractShareInfo("https://pan.baidu.com/share/list", "1", "2", "token"); got["ErrMsg"] == "success" {
			t.Errorf("list accepted %q", body)
		}
		if got := pcs.GenerateRequestQuery("POST", map[string]string{"shareUrl": "https://pan.baidu.com/share/transfer"}); got["ErrNo"] == "0" {
			t.Errorf("transfer accepted %q", body)
		}
	}
}

func TestShareQueriesAcceptValidResponses(t *testing.T) {
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path == "/share/verify" {
			return response(req, `{"errno":0,"randsk":"synthetic"}`), nil
		}
		return response(req, `{"errno":0,"info":[{"path":"/saved/a.txt","fsid":1}]}`), nil
	})
	if got := pcs.PostShareQuery("https://pan.baidu.com/share/verify", "", nil); got["ErrMsg"] != "0" || got["randsk"] != "synthetic" {
		t.Fatal("valid verification failed")
	}
	if got := pcs.GenerateRequestQuery("POST", map[string]string{"shareUrl": "https://pan.baidu.com/share/transfer"}); got["ErrNo"] != "0" || got["filename"] != "a.txt" {
		t.Fatal("valid transfer failed")
	}
}

func TestShareListRequiresItems(t *testing.T) {
	for _, body := range []string{`{"errno":0}`, `{"errno":0,"list":[]}`, `{"errno":0,"list":[null]}`, `{"errno":0,"list":[{"server_filename":"a"}]}`} {
		pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, body), nil })
		if got := pcs.ExtractShareInfo("https://pan.baidu.com/share/list", "1", "2", "token"); got["ErrMsg"] == "success" {
			t.Errorf("invalid list accepted: %q", body)
		}
	}
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		return response(req, `{"errno":0,"list":[{"fs_id":9007199254740993,"server_filename":"a"},{"fs_id":2,"server_filename":"b"}]}`), nil
	})
	got := pcs.ExtractShareInfo("https://pan.baidu.com/share/list", "1", "2", "token")
	if got["ErrMsg"] != "success" || got["fs_id"] != "[9007199254740993,2]" || got["item_num"] != "2" {
		t.Fatalf("valid list failed: %v", got)
	}
}

func TestShareTransferRequiresResultPaths(t *testing.T) {
	for _, body := range []string{`{"errno":0}`, `{"errno":0,"info":[]}`, `{"errno":0,"info":[null]}`, `{"errno":0,"info":[{"path":""}]}`, `{"errno":0,"info":[{"path":12}]}`, `{"errno":0,"info":[{"path":"/"}]}`} {
		pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, body), nil })
		if got := pcs.GenerateRequestQuery("POST", map[string]string{"shareUrl": "https://pan.baidu.com/share/transfer"}); got["ErrNo"] == "0" {
			t.Errorf("incomplete result accepted: %s", body)
		}
	}
}

func TestSharePageReadFailureAndMultiline(t *testing.T) {
	page := "<script>locals.mset({\n\"loginstate\":1,\n\"bdstoken\":\"synthetic\",\n\"shareid\":123,\n\"share_uk\":456\n});</script>"
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) { return response(req, page), nil })
	if got := pcs.AccessSharePage("1fixture", false); got["ErrMsg"] != "0" || got["shareid"] != "123" || got["share_uk"] != "456" {
		t.Errorf("multiline page failed: %v", got)
	}
	body := &shareTrackedBody{Reader: io.MultiReader(strings.NewReader(`{"loginstate":1,"bdstoken":"synthetic","shareid":123,"share_uk":456}`), shareReadFailure{})}
	pcs = fixturePCS(func(req *http.Request) (*http.Response, error) { r := response(req, ""); r.Body = body; return r, nil })
	if got := pcs.AccessSharePage("1fixture", true); got["ErrMsg"] == "0" {
		t.Error("partial page reported as success")
	}
	if !body.closed {
		t.Error("page response was not closed")
	}
}

func TestSharePageRejectsEmptyFeature(t *testing.T) {
	pcs := fixturePCS(func(req *http.Request) (*http.Response, error) {
		t.Error("invalid feature sent request")
		return response(req, ""), nil
	})
	defer func() {
		if recover() != nil {
			t.Fatal("empty feature caused panic")
		}
	}()
	if got := pcs.AccessSharePage("", false); got["ErrMsg"] == "0" {
		t.Error("empty feature accepted")
	}
}
