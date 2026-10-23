package pcsconfig

import (
	"github.com/qjfoidnh/BaiduPCS-Go/requester"
	"os"
	"path/filepath"
	"testing"
)

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
