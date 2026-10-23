package pcsdownload

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/baidupcs"
	"github.com/qjfoidnh/BaiduPCS-Go/pcsutil/taskframework"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

type downloadRoundTrip func(*http.Request) (*http.Response, error)

func (f downloadRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func installDownloadClient(t *testing.T) {
	t.Helper()
	panClientOnce.Do(func() { panClient = requester.NewHTTPClient(); panClient.SetKeepAlive(false) })
	previous := panClient
	client := requester.NewHTTPClient()
	client.SetKeepAlive(false)
	client.Client.Transport = downloadRoundTrip(func(req *http.Request) (*http.Response, error) {
		want := req.URL.Query().Get("account")
		cookie, err := req.Cookie("BDUSS")
		if want == "" {
			if err == nil {
				t.Error("anonymous download inherited account cookies")
			}
		} else if err != nil || cookie.Value != want {
			t.Errorf("download used the wrong account for %s", want)
		}
		return &http.Response{StatusCode: 200, ContentLength: 8, Header: http.Header{"X-Bs-File-Size": []string{"8"}}, Body: io.NopCloser(strings.NewReader("01234567")), Request: req}, nil
	})
	panClient = client
	t.Cleanup(func() { panClient = previous })
}

func downloadTestJar(t *testing.T, host, account string) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u := &url.URL{Scheme: "https", Host: host, Path: "/"}
	jar.SetCookies(u, []*http.Cookie{{Name: "BDUSS", Value: account, Path: "/"}})
	return jar
}

func TestPanDownloadClientsDoNotShareMutableCookieJar(t *testing.T) {
	installDownloadClient(t)
	unit := &DownloadTaskUnit{}
	clientA, clientB, anonymous := unit.panHTTPClient(), unit.panHTTPClient(), unit.panHTTPClient()
	clientA.SetCookiejar(downloadTestJar(t, "fixture.invalid", "SYNTHETIC_A"))
	clientB.SetCookiejar(downloadTestJar(t, "fixture.invalid", "SYNTHETIC_B"))
	anonymous.SetCookiejar(nil)
	for _, request := range []struct {
		client  *requester.HTTPClient
		account string
	}{{clientA, "SYNTHETIC_A"}, {clientB, "SYNTHETIC_B"}, {anonymous, ""}} {
		resp, err := request.client.Req(http.MethodGet, "https://fixture.invalid/?account="+request.account, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
}

func TestPanDownloadUsesTaskAccountAndPCSHost(t *testing.T) {
	installDownloadClient(t)
	for _, source := range []struct{ host, account string }{{"pcs.baidu.com", "SYNTHETIC_A"}, {"account-pcs.fixture.invalid", "SYNTHETIC_B"}, {"pcs.baidu.com", ""}} {
		unit := emptyTask("unused", false, true)
		unit.Cfg.CacheSize = 8192
		unit.Cfg.MaxParallel = 1
		client := requester.NewHTTPClient()
		var jar http.CookieJar
		if source.account != "" {
			jar = downloadTestJar(t, source.host, source.account)
		}
		client.SetCookiejar(jar)
		unit.PCS = baidupcs.NewPCSWithClient(1, client)
		unit.PCS.SetPCSAddr(source.host)
		result := &taskframework.TaskUnitRunResult{}
		ok := false
		unit.execPanDownload("https://fixture.invalid/?account="+source.account, result, &ok)
		if !ok || result.Err != nil {
			t.Fatalf("download with task account failed: %+v", result)
		}
	}
}

func TestCloneDownloadJarValidatesHostsAndKeepsCookies(t *testing.T) {
	source := downloadTestJar(t, "pcs.baidu.com", "SYNTHETIC")
	for _, target := range []string{"https://cdn.fixture.invalid/file", "http://cdn.fixture.invalid/file", "https://127.0.0.1:8443/file", "https://[::1]:8443/file"} {
		jar, err := cloneJarFromHost(source, "pcs.baidu.com", target)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(target)
		if err != nil {
			t.Fatal(err)
		}
		cookies := jar.Cookies(u)
		if len(cookies) != 1 || cookies[0].Name != "BDUSS" || cookies[0].Value != "SYNTHETIC" {
			t.Errorf("cookie clone failed for %s", target)
		}
		if len(source.Cookies(u)) != 0 {
			t.Error("cookie clone modified source jar")
		}
	}
	for _, target := range []string{"%", "/relative", "file:///tmp/data", "https:///file"} {
		if _, err := cloneJarFromHost(source, "pcs.baidu.com", target); err == nil {
			t.Errorf("invalid download URL accepted: %s", target)
		}
	}
	for _, host := range []string{"", "%", "https://pcs.baidu.com", "pcs.baidu.com/path"} {
		if _, err := cloneJarFromHost(source, host, "https://cdn.fixture.invalid/file"); err == nil {
			t.Errorf("invalid source host accepted: %s", host)
		}
	}
}

func TestPanDownloadDoesNotFallBackToGlobalAccount(t *testing.T) {
	installDownloadClient(t)
	unit := emptyTask("unused", false, true)
	result := &taskframework.TaskUnitRunResult{}
	ok := false
	unit.execPanDownload("https://fixture.invalid/", result, &ok)
	if ok || result.Err == nil {
		t.Fatal("download without task account fell back to a global session")
	}
}
