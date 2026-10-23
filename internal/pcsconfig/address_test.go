package pcsconfig

import "testing"

func TestSwitchUserPreservesPCSAddress(t *testing.T) {
	c := reloadFixture(t)
	c.InitDefaultConfig()
	c.SETPCSAddr("c1.pcs.baidu.com")
	c.BaiduUserList = BaiduUserList{&Baidu{BaiduBase: BaiduBase{UID: 7}}}
	if _, err := c.SwitchUser(&BaiduBase{UID: 7}); err != nil {
		t.Fatal(err)
	}
	if c.ActiveUserBaiduPCS().URL().Host != c.PCSAddr {
		t.Fatal("switching account lost configured server address")
	}
}

func TestPCSAddressValidation(t *testing.T) {
	c := reloadFixture(t)
	c.InitDefaultConfig()
	c.pcs = c.ActiveUser().BaiduPCS()
	for _, addr := range []string{"pcs.baidu.com", "c.pcs.baidu.com", "d.pcs.baidu.com", "c1.pcs.baidu.com", "d9.pcs.baidu.com"} {
		if !c.SETPCSAddr(addr) || c.PCSAddr != addr || c.pcs.URL().Host != addr {
			t.Fatalf("valid address rejected or not applied: %q", addr)
		}
	}
	previous := c.PCSAddr
	for _, addr := range []string{
		"pcs.baidu.com.fixture.invalid", "c1.pcs.baidu.com.fixture.invalid",
		"pcs.baidu.com@fixture.invalid", "pcs.baidu.com/path", "pcs.baidu.com:443",
		"pcs.baidu.com?query", "pcs.baidu.com#fragment", "pcs.baidu.com\n",
		"https://pcs.baidu.com", "fixture.invalid", "",
	} {
		if c.SETPCSAddr(addr) {
			t.Errorf("invalid address accepted: %q", addr)
		}
		if c.PCSAddr != previous || c.pcs.URL().Host != previous {
			t.Errorf("invalid input changed configuration or client: %q", addr)
		}
	}
}
