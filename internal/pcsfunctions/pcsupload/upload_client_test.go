package pcsupload

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/qjfoidnh/BaiduPCS-Go/requester"
)

func syntheticUploadJar(t *testing.T, account string) http.CookieJar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://fixture.invalid/")
	jar.SetCookies(u, []*http.Cookie{{Name: "BDUSS", Value: account, Path: "/"}})
	return jar
}

func installAccountCheckingTransport(t *testing.T) {
	t.Helper()
	testPCSUploadClient(t, func(req *http.Request) (*http.Response, error) {
		want := req.URL.Query().Get("account")
		cookie, err := req.Cookie("BDUSS")
		if want == "" {
			if err == nil {
				t.Error("anonymous upload inherited account cookies")
			}
		} else if err != nil || cookie.Value != want {
			t.Errorf("upload used another account's session for %s", want)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Set-Cookie": []string{"seen=" + want + "; Path=/"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
	})
}

func TestUploadClientsKeepAccountCookiesSeparate(t *testing.T) {
	installAccountCheckingTransport(t)
	jarA, jarB := syntheticUploadJar(t, "SYNTHETIC_A"), syntheticUploadJar(t, "SYNTHETIC_B")
	uploadClient.SetCookiejar(jarA)
	clientA, clientB, anonymous := getUploadClient(jarA), getUploadClient(jarB), getUploadClient(nil)
	for _, request := range []struct {
		client  *requester.HTTPClient
		account string
	}{
		{clientA, "SYNTHETIC_A"}, {clientB, "SYNTHETIC_B"}, {anonymous, ""}, {clientA, "SYNTHETIC_A"},
	} {
		resp, err := request.client.Req(http.MethodGet, "https://fixture.invalid/?account="+request.account, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	u, _ := url.Parse("https://fixture.invalid/")
	for account, jar := range map[string]http.CookieJar{"SYNTHETIC_A": jarA, "SYNTHETIC_B": jarB} {
		found := false
		for _, cookie := range jar.Cookies(u) {
			if cookie.Name == "seen" {
				found = true
				if cookie.Value != account {
					t.Error("response cookie was stored in another account's jar")
				}
			}
		}
		if !found {
			t.Error("response cookie was not retained by its account")
		}
	}
}

func TestConcurrentUploadClientsUseTheirOwnSessions(t *testing.T) {
	installAccountCheckingTransport(t)
	const count = 24
	var ready, done sync.WaitGroup
	ready.Add(count)
	done.Add(count)
	start := make(chan struct{})
	for i := 0; i < count; i++ {
		account := fmt.Sprintf("SYNTHETIC_%d", i)
		jar := syntheticUploadJar(t, account)
		go func() {
			defer done.Done()
			client := getUploadClient(jar)
			ready.Done()
			<-start
			resp, err := client.Req(http.MethodGet, "https://fixture.invalid/?account="+account, nil, nil)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}
