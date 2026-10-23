package pcsconfig

import (
	"errors"
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigResetsOptionsPreservingAccounts(t *testing.T) {
	c := NewConfig("")
	account := &Baidu{BaiduBase: BaiduBase{UID: 7}}
	c.BaiduActiveUID, c.BaiduUserList = 7, BaiduUserList{account}
	c.MaxDownloadRate, c.MaxUploadRate = 123, 456
	c.ProxyHostnames, c.FixPCSAddr = "fixture.invalid", true
	c.InitDefaultConfig()
	if c.MaxDownloadRate != 0 || c.MaxUploadRate != 0 || c.ProxyHostnames != "" || c.FixPCSAddr {
		t.Error("reset retained non-default transfer options")
	}
	if c.BaiduActiveUID != 7 || len(c.BaiduUserList) != 1 || c.BaiduUserList[0] != account {
		t.Error("reset changed accounts")
	}
}

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReloadAppliesStaticServerOption(t *testing.T) {
	c := reloadFixture(t)
	for _, enabled := range []bool{true, false} {
		data := `{}`
		if enabled {
			data = `{"fix_pcs_addr":true}`
		}
		writeReloadFixture(t, c, data)
		if err := c.Reload(); err != nil {
			t.Fatal(err)
		}
		pcs := c.ActiveUserBaiduPCS()
		client := pcs.GetClient()
		client.SetProxy("") // Initialize the internal transport before substituting it.
		called := false
		client.Client.Transport = fixtureTransport(func(*http.Request) (*http.Response, error) {
			called = true
			return nil, errors.New("synthetic server discovery failure")
		})
		err, host := pcs.GetRandomPCSHost()
		if enabled && (called || err != nil || host != "") {
			t.Fatal("fixed server mode attempted dynamic server discovery")
		}
		if !enabled && (!called || err == nil) {
			t.Fatal("removed option did not restore dynamic server discovery")
		}
	}
}

func reloadFixture(t *testing.T) *PCSConfig {
	t.Helper()
	oldConfig := Config
	oldUA := requester.UserAgent
	oldProxy := requester.ProxyAddr
	oldRules := requester.ProxyHostnameRules
	c := NewConfig(filepath.Join(t.TempDir(), "config.json"))
	Config = c
	t.Cleanup(func() {
		c.Close()
		Config = oldConfig
		requester.UserAgent = oldUA
		requester.SetGlobalProxy(oldProxy)
		requester.SetProxyHostnameRules(oldRules)
	})
	return c
}
func writeReloadFixture(t *testing.T, c *PCSConfig, data string) {
	t.Helper()
	if err := os.WriteFile(c.FilePath(), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestReloadRefreshesSameAccountClient(t *testing.T) {
	c := reloadFixture(t)
	writeReloadFixture(t, c, `{"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"bduss":"SYNTHETIC_OLD","stoken":"SYNTHETIC_OLD"}],"user_agent":"fixture-old"}`)
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	oldClient := c.ActiveUserBaiduPCS()
	writeReloadFixture(t, c, `{"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"bduss":"SYNTHETIC_NEW"}],"user_agent":"fixture-new","proxy":"http://127.0.0.1:1","proxy_hostnames":"fixture.invalid"}`)
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.ActiveUserBaiduPCS() == oldClient || c.ActiveUserBaiduPCS().GetBDUSS() != "SYNTHETIC_NEW" {
		t.Error("same-UID reload retained old client")
	}
	if c.ActiveUser().STOKEN != "" {
		t.Error("removed credential retained")
	}
	if requester.UserAgent != "fixture-new" || requester.ProxyAddr != "http://127.0.0.1:1" {
		t.Error("runtime settings not reapplied")
	}
}
func TestReloadClearsOmittedAccounts(t *testing.T) {
	c := reloadFixture(t)
	writeReloadFixture(t, c, `{"baidu_active_uid":7,"baidu_user_list":[{"uid":7,"bduss":"SYNTHETIC"}]}`)
	if err := c.Init(); err != nil {
		t.Fatal(err)
	}
	writeReloadFixture(t, c, `{}`)
	if err := c.Reload(); err != nil {
		t.Fatal(err)
	}
	if c.ActiveUser().UID != 0 || c.BaiduActiveUID != 0 || len(c.BaiduUserList) != 0 || c.ActiveUserBaiduPCS().GetBDUSS() != "" {
		t.Fatal("omitted accounts survived reload")
	}
}
